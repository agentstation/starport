package availability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/agentstation/starport/internal/failure"
)

// ErrInvalidSharedStore reports a missing distributed store.
var ErrInvalidSharedStore = errors.New("shared availability store is required")

// KVStore is the storage subset that shared health state uses. The
// deployment's distributed key-value store satisfies it. A tracker without
// one stays process-local.
type KVStore interface {
	SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error
	ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error)
	BatchGet(ctx context.Context, keys []string) (map[string][]byte, error)
}

const (
	// DefaultSharedTTL bounds how long a replica's health record outlives
	// its last transition. A dead replica's record expires on its own.
	DefaultSharedTTL = time.Minute
	// DefaultSharedRefreshInterval bounds the peer read cost. A tracker
	// reads peer state at most once per interval.
	DefaultSharedRefreshInterval = 5 * time.Second

	sharedHealthKeyPrefix = "provider-health:instance:"
	sharedScanLimit       = 1024
)

// SharedConfig bounds the distributed health exchange.
type SharedConfig struct {
	// InstanceID names this replica's record. An empty value draws a
	// random identity.
	InstanceID string
	// TTL is the record lifetime in the shared store.
	TTL time.Duration
	// RefreshInterval is the shortest gap between two peer reads.
	RefreshInterval time.Duration
}

func (c SharedConfig) withDefaults() SharedConfig {
	if c.InstanceID == "" {
		c.InstanceID = NewInstanceID()
	}
	if c.TTL <= 0 {
		c.TTL = DefaultSharedTTL
	}
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = DefaultSharedRefreshInterval
	}
	return c
}

// NewInstanceID returns a random replica identity for shared health records.
func NewInstanceID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("pid-%d", os.Getpid())
	}
	return hex.EncodeToString(raw[:])
}

// sharedDocument is one replica's published health state.
type sharedDocument struct {
	InstanceID string         `json:"instance_id"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Records    []sharedRecord `json:"records"`
}

// sharedRecord is one offering state with the recency evidence that decides
// merge conflicts.
type sharedRecord struct {
	ProviderID         string    `json:"provider_id"`
	ProviderModelID    string    `json:"provider_model_id"`
	State              State     `json:"state"`
	FailureKind        string    `json:"failure_kind,omitempty"`
	ConsecutiveFailure int       `json:"consecutive_failure,omitempty"`
	OpenUntil          time.Time `json:"open_until,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// UseSharedStore turns on distributed health publication. The tracker writes
// its state under its own instance key on every transition and merges peer
// state during Refresh. Local state wins recency conflicts.
func (t *Tracker) UseSharedStore(store KVStore, config SharedConfig) error {
	if store == nil {
		return ErrInvalidSharedStore
	}
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.shared = store
	t.sharedConfig = config.withDefaults()
	t.mu.Unlock()
	return nil
}

// sharedDocumentLocked projects the local records into this replica's
// publication, or nil when no shared store is configured.
func (t *Tracker) sharedDocumentLocked() *sharedDocument {
	if t.shared == nil {
		return nil
	}
	doc := &sharedDocument{
		InstanceID: t.sharedConfig.InstanceID,
		UpdatedAt:  t.clock.Now(),
		Records:    make([]sharedRecord, 0, len(t.records)),
	}
	for offering, entry := range t.records {
		doc.Records = append(doc.Records, sharedRecord{
			ProviderID:         offering.ProviderID,
			ProviderModelID:    offering.ProviderModelID,
			State:              entry.state,
			FailureKind:        string(entry.failureKind),
			ConsecutiveFailure: entry.consecutiveFailure,
			OpenUntil:          entry.openUntil,
			UpdatedAt:          entry.updatedAt,
		})
	}
	sort.Slice(doc.Records, func(left, right int) bool {
		if doc.Records[left].ProviderID != doc.Records[right].ProviderID {
			return doc.Records[left].ProviderID < doc.Records[right].ProviderID
		}
		return doc.Records[left].ProviderModelID < doc.Records[right].ProviderModelID
	})
	return doc
}

// writeShared publishes one replica document with the configured TTL.
func (t *Tracker) writeShared(doc *sharedDocument) {
	if doc == nil {
		return
	}
	t.mu.Lock()
	store := t.shared
	ttl := t.sharedConfig.TTL
	t.mu.Unlock()
	if store == nil {
		return
	}
	data, err := json.Marshal(doc)
	if err == nil {
		err = store.SetWithTTL(context.Background(), sharedHealthKeyPrefix+doc.InstanceID, data, ttl)
	}
	if err != nil {
		t.recordSharedError(err)
	}
}

// mergePeerState reads every replica document at most once per refresh
// interval and adopts the peer records that carry newer evidence.
func (t *Tracker) mergePeerState(ctx context.Context) {
	t.mu.Lock()
	store := t.shared
	if store == nil {
		t.mu.Unlock()
		return
	}
	now := t.clock.Now()
	if !t.lastPeerFetch.IsZero() && now.Sub(t.lastPeerFetch) < t.sharedConfig.RefreshInterval {
		t.mu.Unlock()
		return
	}
	t.lastPeerFetch = now
	own := t.sharedConfig.InstanceID
	t.mu.Unlock()

	keys, err := store.ScanWithPrefix(ctx, sharedHealthKeyPrefix, sharedScanLimit)
	if err != nil {
		t.recordSharedError(err)
		return
	}
	if len(keys) == 0 {
		return
	}
	values, err := store.BatchGet(ctx, keys)
	if err != nil {
		t.recordSharedError(err)
		return
	}
	documents := make([]sharedDocument, 0, len(values))
	for _, raw := range values {
		var doc sharedDocument
		if json.Unmarshal(raw, &doc) != nil || doc.InstanceID == own {
			continue
		}
		documents = append(documents, doc)
	}
	if len(documents) == 0 {
		return
	}

	t.mu.Lock()
	changed := false
	for _, doc := range documents {
		for _, record := range doc.Records {
			changed = t.adoptRecordLocked(record) || changed
		}
	}
	var snapshot Snapshot
	var ownDoc *sharedDocument
	if changed {
		snapshot = t.changedSnapshotLocked()
		ownDoc = t.sharedDocumentLocked()
	}
	t.mu.Unlock()
	if changed {
		t.publish(snapshot)
		t.writeShared(ownDoc)
	}
}

// adoptRecordLocked applies one peer record. The local record wins when its
// own transition is at least as recent as the peer evidence.
func (t *Tracker) adoptRecordLocked(record sharedRecord) bool {
	offering := Offering{ProviderID: record.ProviderID, ProviderModelID: record.ProviderModelID}
	if validateOffering(offering) != nil {
		return false
	}
	switch record.State {
	case StateHealthy, StateOpen, StateHalfOpen, StateUnavailable:
	default:
		return false
	}
	existing := t.records[offering]
	if existing != nil && !existing.updatedAt.Before(record.UpdatedAt) {
		return false
	}
	if existing == nil {
		if record.State == StateHealthy {
			return false
		}
		value := entryValue(StateHealthy)
		existing = &value
		t.records[offering] = existing
	}
	existing.state = record.State
	existing.failureKind = failure.Kind(record.FailureKind)
	existing.consecutiveFailure = record.ConsecutiveFailure
	existing.openUntil = record.OpenUntil
	existing.probeInFlight = false
	existing.updatedAt = record.UpdatedAt
	return true
}

func (t *Tracker) recordSharedError(err error) {
	t.mu.Lock()
	t.lastError = err
	t.mu.Unlock()
}
