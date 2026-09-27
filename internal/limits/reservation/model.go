package reservation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrInvalid reports incomplete or inconsistent reservation input.
	ErrInvalid = errors.New("invalid budget reservation")
	// ErrHistoryUnknown refuses to infer zero consumption from an absent meter.
	ErrHistoryUnknown = errors.New("budget consumption history is unknown")
	// ErrExhausted reports insufficient exclusive capacity in an applicable meter.
	ErrExhausted = errors.New("budget capacity is exhausted")
	// ErrIdentityConflict reports reuse of an identity with different evidence.
	ErrIdentityConflict = errors.New("reservation identity has different evidence")
	// ErrAlreadyDispatched prevents a retry from spending a dispatch permit twice.
	ErrAlreadyDispatched = errors.New("reservation dispatch permission was consumed")
	// ErrTransition reports a state that does not permit the requested action.
	ErrTransition = errors.New("invalid reservation state transition")
	// ErrUnavailable reports malformed or expiring required state.
	ErrUnavailable = errors.New("required budget state is unavailable")
)

// Meter names an enduring population, dimension, and fixed UTC interval.
// Limit and policy revision do not form part of its storage identity.
type Meter struct {
	Scope     limits.Scope     `json:"scope"`
	Holder    string           `json:"holder"`
	Dimension limits.Dimension `json:"dimension"`
	Interval  string           `json:"interval"`
}

// Rule binds the current limit and policy evidence to a meter.
type Rule struct {
	Meter          Meter  `json:"meter"`
	Limit          int64  `json:"limit"`
	PolicyRevision string `json:"policy_revision"`
	HistoryID      string `json:"history_id"`
}

// Attempt names one potentially charged dispatch and its enforced upper bounds.
// Every retry and child operation has a distinct ID.
type Attempt struct {
	ID                string     `json:"id"`
	RequestID         string     `json:"request_id"`
	AccountID         string     `json:"account_id"`
	KeyID             string     `json:"key_id"`
	TeamID            string     `json:"team_id,omitempty"`
	OfferingID        string     `json:"offering_id"`
	CatalogGeneration string     `json:"catalog_generation"`
	Operation         string     `json:"operation"`
	Rules             []Rule     `json:"rules"`
	Valuation         Valuation  `json:"valuation"`
	Bound             Quantities `json:"bound"`
	TokenBound        int64      `json:"token_bound"`
	// TokenOnly records unknown monetary cost when every required meter counts
	// tokens. It cannot bypass a spend meter or represent unknown tokens as zero.
	TokenOnly bool `json:"token_only,omitempty"`
}

// State names a persisted attempt transition.
type State string

const (
	// Reserved holds capacity before dispatch permission has been consumed.
	Reserved State = "reserved"
	// Dispatched means exactly one caller consumed dispatch permission.
	Dispatched State = "dispatched"
	// Uncertain retains the full reservation pending reliable charge evidence.
	Uncertain State = "uncertain"
	// Settled retains the identity and the final charge after reconciliation.
	Settled State = "settled"
	// Canceled releases capacity before any dispatch permission was consumed.
	Canceled State = "canceled"
)

// Binding fixes a meter's original window and deducted capacity.
type Binding struct {
	Rule   Rule               `json:"rule"`
	Window storage.TimeWindow `json:"window"`
	Amount int64              `json:"amount"`
}

// Evidence must describe reliable provider usage or explicit operator reconciliation.
// A timeout, terminal job state, or local token estimate is not charge evidence.
type Evidence struct {
	ID         string     `json:"id"`
	Quantities Quantities `json:"quantities"`
	Tokens     int64      `json:"tokens"`
}

// Record preserves the pinned valuation, original windows, and settlement identity.
// No reservation record expires automatically in this implementation.
type Record struct {
	Version    int       `json:"version"`
	Attempt    Attempt   `json:"attempt"`
	State      State     `json:"state"`
	AdmittedAt time.Time `json:"admitted_at"`
	Bindings   []Binding `json:"bindings"`
	Evidence   *Evidence `json:"evidence,omitempty"`
	Unresolved *Evidence `json:"unresolved,omitempty"`
	Pending    *Evidence `json:"pending,omitempty"`
	// NanoUSD is null when only tokens were metered and monetary cost is unknown.
	NanoUSD *int64 `json:"nano_usd"`
	Reason  string `json:"reason,omitempty"`
	// JobID binds this attempt to one asynchronous job before provider dispatch.
	JobID string `json:"job_id,omitempty"`
}

// WindowState contains verified consumption and reserved capacity for one meter.
// Overflow blocks new admission until explicit reconciliation repairs the aggregate.
type WindowState struct {
	Version      int                `json:"version"`
	Meter        Meter              `json:"meter"`
	Window       storage.TimeWindow `json:"window"`
	HistoryProof string             `json:"history_proof"`
	HistoryID    string             `json:"history_id"`
	SeedConsumed int64              `json:"seed_consumed"`
	Consumed     int64              `json:"consumed"`
	Reserved     int64              `json:"reserved"`
	Overflow     bool               `json:"overflow,omitempty"`
}

func validID(id string) bool {
	return len(id) > 0 && len(id) <= 256 && strings.IndexFunc(id, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func (m Meter) valid() bool {
	return (m.Scope == limits.ScopeAccount || m.Scope == limits.ScopeKey || m.Scope == limits.ScopeTeam) &&
		validID(m.Holder) && (m.Dimension == limits.DimensionSpend || m.Dimension == limits.DimensionTokens) && limits.ValidInterval(m.Interval)
}

func windowFor(interval string, at time.Time) storage.TimeWindow {
	at = at.UTC()
	start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	switch interval {
	case limits.IntervalWeek:
		start = start.AddDate(0, 0, -(int(at.Weekday())+6)%7)
		return storage.TimeWindow{Start: start, End: start.AddDate(0, 0, 7)}
	case limits.IntervalMonth:
		start = time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
		return storage.TimeWindow{Start: start, End: start.AddDate(0, 1, 0)}
	default:
		return storage.TimeWindow{Start: start, End: start.AddDate(0, 0, 1)}
	}
}

func storageKey(kind string, value any) string {
	// All callers pass the fixed record types in this package.
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return "budget:v1:" + kind + ":" + hex.EncodeToString(digest[:])
}

func meterKey(meter Meter, window storage.TimeWindow) string {
	return storageKey("meter", struct {
		Meter  Meter
		Window storage.TimeWindow
	}{meter, window})
}
