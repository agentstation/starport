package usage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

const (
	// StorageSchemaVersion identifies the only usage record schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the usage v1 namespace.
	StoragePrefix = "usage:v1:"
	// recordPrefix holds per-request records, keyed for a newest-first
	// scan within one key: usage:v1:record:<b64 keyID>:<20-digit
	// zero-padded unix nanos>:<b64 requestID>.
	recordPrefix = StoragePrefix + "record:"
	// aggregatePrefix holds atomic window counters.
	aggregatePrefix = StoragePrefix + "agg:"

	// DefaultRetention bounds how long records and counters survive.
	DefaultRetention = 30 * 24 * time.Hour
	// DefaultListLimit applies when a query gives no limit.
	DefaultListLimit = 50
	// MaxListLimit bounds one page.
	MaxListLimit = 1000

	// batchSize bounds one value fetch while filling a page.
	batchSize = 256

	// Aggregate counter names.
	counterRequests = "requests"
	counterTokens   = "tokens"
	counterSpend    = "spend"
)

// Intervals for aggregate windows. Windows are fixed and UTC-aligned.
const (
	IntervalDay   = "day"
	IntervalWeek  = "week"
	IntervalMonth = "month"
)

// Aggregate scope kinds. They are storage-key segments, so they never change
// without a schema version.
const (
	scopeKindGateway = "all"
	scopeKindKey     = "key"
	scopeKindAccount = "account"
	scopeKindTeam    = "team"
)

var (
	// ErrRepositoryRequired reports an absent usage storage adapter.
	ErrRepositoryRequired = errors.New("usage storage is required")
	// ErrInvalidQuery reports an unusable list query.
	ErrInvalidQuery = errors.New("invalid usage query")
	// ErrInvalidInterval reports an unknown aggregate interval.
	ErrInvalidInterval = errors.New("invalid usage interval")
	// ErrInvalidScope reports an aggregate scope that names no subject.
	ErrInvalidScope = errors.New("invalid usage scope")
	// ErrCorruptRecord reports invalid durable usage data.
	ErrCorruptRecord = errors.New("usage record is invalid")
)

// Scope selects which counter set an aggregate query reads. The gateway keeps
// one set per key, one per account, and one for the whole deployment, because
// an account cap and a key cap meter different populations: the account total
// is the sum over every key it holds, so neither can be derived from the other.
//
// It is comparable, so a caller may use it as a map key.
type Scope struct {
	kind string
	id   string
}

// KeyScope addresses the counters for one gateway API key.
func KeyScope(keyID string) Scope { return Scope{kind: scopeKindKey, id: keyID} }

// AccountScope addresses the counters for one account: every key it holds.
func AccountScope(accountID string) Scope { return Scope{kind: scopeKindAccount, id: accountID} }

// TeamScope addresses the counters for one team: every key attributed to it,
// across every account the team reaches.
func TeamScope(teamID string) Scope { return Scope{kind: scopeKindTeam, id: teamID} }

// GatewayScope addresses the counters for the whole deployment.
func GatewayScope() Scope { return Scope{kind: scopeKindGateway} }

// String names the scope for a log field.
func (s Scope) String() string {
	if s.kind == scopeKindGateway || s.kind == "" {
		return scopeKindGateway
	}
	return s.kind + ":" + s.id
}

// valid reports whether the scope names a readable counter set. A key or
// account scope without a subject would silently read the wrong counters, so
// it is an error rather than a fallback.
func (s Scope) valid() bool {
	switch s.kind {
	case scopeKindGateway:
		return true
	case scopeKindKey, scopeKindAccount, scopeKindTeam:
		return s.id != ""
	}
	return false
}

// storageSegment is the scope's counter-key segment.
func (s Scope) storageSegment() string {
	if s.kind == scopeKindGateway {
		return scopeKindGateway
	}
	return s.kind + ":" + encodeSegment(s.id)
}

// Query selects records. An empty KeyID selects every key and an empty AccountID
// every account (admin scope); callers own that authorization decision.
type Query struct {
	KeyID string
	// AccountID selects one account's records across every key it holds.
	// Records are keyed by gateway API key, so a query that names only an
	// account scans the record namespace and filters. That cost belongs to
	// reporting: budget enforcement reads the aggregate counters instead.
	AccountID string
	Model     string
	Provider  string
	Status    string
	// RequestID selects the one record a request left, so an audit record
	// or a log line reaches its usage row.
	RequestID string
	// GuardrailVerdict keeps only records a guardrail closed with this
	// verdict: `refuse` or `redact`. Empty places no filter.
	GuardrailVerdict string
	Since            time.Time
	Until            time.Time
	Limit            int
	// Cursor continues a previous page: the opaque NextCursor value.
	Cursor string
}

// Page is one newest-first result page.
type Page struct {
	Records []Record
	// NextCursor continues the listing; empty when the page is the last.
	NextCursor string
}

// Totals are the aggregate counters for one scope and window.
type Totals struct {
	Requests     int64
	Tokens       int64
	SpendNanoUSD int64
}

// Repository is the durable usage contract.
type Repository interface {
	// Put persists one record and advances the aggregate counters.
	Put(context.Context, Record) error
	// List returns records newest-first.
	List(context.Context, Query) (Page, error)
	// Totals reads the aggregate counters for one scope in the window
	// containing at.
	Totals(ctx context.Context, scope Scope, interval string, at time.Time) (Totals, error)
}

// Options configure a repository.
type Options struct {
	// Retention bounds record and counter lifetime; DefaultRetention
	// when zero.
	Retention time.Duration
}

type repository struct {
	store     storage.KVStore
	retention time.Duration
}

type storedRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Record        Record `json:"record"`
}

// Open returns a storage-backed usage repository.
func Open(store storage.KVStore, options Options) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	retention := options.Retention
	if retention <= 0 {
		retention = DefaultRetention
	}
	return &repository{store: store, retention: retention}, nil
}

func (r *repository) Put(ctx context.Context, record Record) error {
	if err := record.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(storedRecord{SchemaVersion: StorageSchemaVersion, Record: record})
	if err != nil {
		return fmt.Errorf("encode usage record: %w", err)
	}
	if err := r.store.SetWithTTL(ctx, recordKey(record.KeyID, record.Timestamp, record.RequestID), data, r.retention); err != nil {
		return fmt.Errorf("put usage record: %w", err)
	}
	return r.accumulate(ctx, record)
}

func (r *repository) accumulate(ctx context.Context, record Record) error {
	var spend int64
	if record.Cost != nil {
		spend = record.Cost.NanoUSD
	}
	counters := []struct {
		name  string
		delta int64
	}{
		{counterRequests, 1},
		{counterTokens, record.Tokens.Total},
		{counterSpend, spend},
	}
	// A record advances one counter set per population that can cap it. The
	// account set is skipped for a record written before account attribution
	// existed: counting it under no account is worse than not counting it.
	scopes := []Scope{KeyScope(record.KeyID), GatewayScope()}
	if record.AccountID != "" {
		scopes = append(scopes, AccountScope(record.AccountID))
	}
	if record.TeamID != "" {
		scopes = append(scopes, TeamScope(record.TeamID))
	}
	for _, scope := range scopes {
		for _, interval := range []string{IntervalDay, IntervalWeek, IntervalMonth} {
			start, end := window(interval, record.Timestamp)
			for _, counter := range counters {
				if counter.delta == 0 && counter.name != counterRequests {
					continue
				}
				key := aggregateKey(scope, interval, start, counter.name)
				value, err := r.store.Increment(ctx, key, counter.delta)
				if err != nil {
					return fmt.Errorf("advance usage counter: %w", err)
				}
				if value == counter.delta {
					// First write in this window: bound its lifetime.
					if err := r.store.ExpireAt(ctx, key, end.Add(r.retention)); err != nil {
						return fmt.Errorf("expire usage counter: %w", err)
					}
				}
			}
		}
	}
	return nil
}

func (r *repository) List(ctx context.Context, query Query) (Page, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	prefix := recordPrefix
	if query.KeyID != "" {
		prefix += encodeSegment(query.KeyID) + ":"
	}
	keys, err := r.store.ScanWithPrefix(ctx, prefix, 0)
	if err != nil {
		return Page{}, fmt.Errorf("scan usage records: %w", err)
	}
	ordered := orderKeysNewestFirst(keys)
	ordered = trimToCursor(ordered, query.Cursor)
	ordered = trimToTimeRange(ordered, query.Since, query.Until)

	page := Page{Records: make([]Record, 0, limit)}
	for start := 0; start < len(ordered) && len(page.Records) < limit; start += batchSize {
		stop := min(start+batchSize, len(ordered))
		batch := make([]string, 0, stop-start)
		for _, parsed := range ordered[start:stop] {
			batch = append(batch, parsed.key)
		}
		values, err := r.store.BatchGet(ctx, batch)
		if err != nil {
			return Page{}, fmt.Errorf("read usage records: %w", err)
		}
		for _, parsed := range ordered[start:stop] {
			data, ok := values[parsed.key]
			if !ok {
				// The record expired between scan and read.
				continue
			}
			record, err := decodeRecord(data)
			if err != nil {
				return Page{}, err
			}
			if !matches(record, query) {
				continue
			}
			page.Records = append(page.Records, record)
			if len(page.Records) == limit {
				if parsed != ordered[len(ordered)-1] {
					page.NextCursor = encodeCursor(parsed.key)
				}
				break
			}
		}
	}
	return page, nil
}

func (r *repository) Totals(ctx context.Context, scope Scope, interval string, at time.Time) (Totals, error) {
	if !validInterval(interval) {
		return Totals{}, fmt.Errorf("%w: %q", ErrInvalidInterval, interval)
	}
	if !scope.valid() {
		return Totals{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope.String())
	}
	start, _ := window(interval, at)
	totals := Totals{}
	for _, counter := range []struct {
		name  string
		value *int64
	}{
		{counterRequests, &totals.Requests},
		{counterTokens, &totals.Tokens},
		{counterSpend, &totals.SpendNanoUSD},
	} {
		data, err := r.store.Get(ctx, aggregateKey(scope, interval, start, counter.name))
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return Totals{}, fmt.Errorf("read usage counter: %w", err)
		}
		value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			return Totals{}, fmt.Errorf("%w: counter %s is not numeric", ErrCorruptRecord, counter.name)
		}
		*counter.value = value
	}
	return totals, nil
}

// parsedKey is one scanned record key with its embedded timestamp.
type parsedKey struct {
	key   string
	nanos int64
}

func orderKeysNewestFirst(keys []string) []parsedKey {
	ordered := make([]parsedKey, 0, len(keys))
	for _, key := range keys {
		nanos, ok := recordKeyNanos(key)
		if !ok {
			continue
		}
		ordered = append(ordered, parsedKey{key: key, nanos: nanos})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].nanos != ordered[j].nanos {
			return ordered[i].nanos > ordered[j].nanos
		}
		return ordered[i].key > ordered[j].key
	})
	return ordered
}

func trimToCursor(ordered []parsedKey, cursor string) []parsedKey {
	if cursor == "" {
		return ordered
	}
	afterKey, err := decodeCursor(cursor)
	if err != nil {
		return nil
	}
	for i, parsed := range ordered {
		if parsed.key == afterKey {
			return ordered[i+1:]
		}
	}
	// The cursor record expired; resume strictly after its position.
	nanos, ok := recordKeyNanos(afterKey)
	if !ok {
		return nil
	}
	for i, parsed := range ordered {
		if parsed.nanos < nanos || (parsed.nanos == nanos && parsed.key < afterKey) {
			return ordered[i:]
		}
	}
	return nil
}

func trimToTimeRange(ordered []parsedKey, since, until time.Time) []parsedKey {
	trimmed := ordered[:0:0]
	for _, parsed := range ordered {
		at := time.Unix(0, parsed.nanos)
		if !since.IsZero() && at.Before(since) {
			continue
		}
		if !until.IsZero() && at.After(until) {
			continue
		}
		trimmed = append(trimmed, parsed)
	}
	return trimmed
}

func matches(record Record, query Query) bool {
	if query.AccountID != "" && record.AccountID != query.AccountID {
		return false
	}
	if query.Model != "" && record.ModelUsed != query.Model && record.ModelRequested != query.Model {
		return false
	}
	if query.Provider != "" && record.Provider != query.Provider {
		return false
	}
	if query.Status != "" && record.Status != query.Status {
		return false
	}
	if query.RequestID != "" && record.RequestID != query.RequestID {
		return false
	}
	if query.GuardrailVerdict != "" && record.GuardrailVerdict != query.GuardrailVerdict {
		return false
	}
	return true
}

func decodeRecord(data []byte) (Record, error) {
	var stored storedRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return Record{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion {
		return Record{}, fmt.Errorf("%w: unsupported schema", ErrCorruptRecord)
	}
	if err := stored.Record.Validate(); err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return stored.Record, nil
}

func recordKey(keyID string, at time.Time, requestID string) string {
	return recordPrefix + encodeSegment(keyID) + ":" + fmt.Sprintf("%020d", at.UnixNano()) + ":" + encodeSegment(requestID)
}

// recordKeyNanos extracts the timestamp segment from a record key.
func recordKeyNanos(key string) (int64, bool) {
	rest, ok := strings.CutPrefix(key, recordPrefix)
	if !ok {
		return 0, false
	}
	segments := strings.Split(rest, ":")
	if len(segments) != 3 {
		return 0, false
	}
	nanos, err := strconv.ParseInt(segments[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return nanos, true
}

func aggregateKey(scope Scope, interval string, start time.Time, counter string) string {
	return aggregatePrefix + scope.storageSegment() + ":" + interval + ":" +
		strconv.FormatInt(start.Unix(), 10) + ":" + counter
}

func encodeSegment(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func encodeCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func decodeCursor(cursor string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(data), recordPrefix) {
		return "", fmt.Errorf("%w: bad cursor", ErrInvalidQuery)
	}
	return string(data), nil
}

func validInterval(interval string) bool {
	switch interval {
	case IntervalDay, IntervalWeek, IntervalMonth:
		return true
	}
	return false
}

// Window returns the UTC-aligned [start, end) of the interval containing
// at. Callers use the end to report when a fixed budget window resets.
func Window(interval string, at time.Time) (time.Time, time.Time) {
	return window(interval, at)
}

// window returns the UTC-aligned [start, end) of the interval containing at.
func window(interval string, at time.Time) (time.Time, time.Time) {
	at = at.UTC()
	switch interval {
	case IntervalWeek:
		// ISO week: Monday start.
		weekday := (int(at.Weekday()) + 6) % 7
		start := time.Date(at.Year(), at.Month(), at.Day()-weekday, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 7)
	case IntervalMonth:
		start := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, 0)
	default:
		start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 1)
	}
}
