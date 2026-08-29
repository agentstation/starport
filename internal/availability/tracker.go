// Package availability owns runtime state for exact provider offerings.
package availability

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/routing"
)

var (
	// ErrInvalidOffering reports an incomplete offering identity.
	ErrInvalidOffering = errors.New("provider offering identity is incomplete")
	// ErrInvalidConfig reports an invalid availability policy.
	ErrInvalidConfig = errors.New("availability configuration is invalid")
)

// State identifies one offering's runtime availability state.
type State string

const (
	// StateHealthy admits normal attempts for an offering.
	StateHealthy State = "healthy"
	// StateOpen rejects attempts until the open interval expires.
	StateOpen State = "open"
	// StateHalfOpen admits one recovery probe.
	StateHalfOpen State = "half_open"
	// StateUnavailable rejects attempts until an explicit reset.
	StateUnavailable State = "unavailable"
)

// Offering identifies one provider model endpoint.
type Offering struct {
	ProviderID      string
	ProviderModelID string
}

// Config defines the offering circuit policy.
type Config struct {
	FailureThreshold int
	OpenDuration     time.Duration
}

// DefaultConfig returns the production availability policy.
func DefaultConfig() Config {
	return Config{FailureThreshold: 3, OpenDuration: 30 * time.Second}
}

// Clock supplies deterministic availability time.
type Clock interface {
	Now() time.Time
}

// Publisher receives immutable availability state for derived projections.
type Publisher interface {
	PublishAvailability(Snapshot) error
}

// Record is one immutable offering state record.
type Record struct {
	Offering           Offering
	State              State
	FailureKind        failure.Kind
	ConsecutiveFailure int
	OpenUntil          time.Time
}

// Snapshot is one immutable availability generation.
type Snapshot struct {
	Revision uint64
	Records  []Record
}

// Tracker owns all offering state transitions and half-open probe admission.
type Tracker struct {
	mu sync.Mutex

	config    Config
	clock     Clock
	publisher Publisher
	revision  uint64
	records   map[Offering]*entry
	lastError error

	publishMu         sync.Mutex
	publishedRevision uint64
}

type entry struct {
	state              State
	failureKind        failure.Kind
	consecutiveFailure int
	openUntil          time.Time
	probeInFlight      bool
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// New creates one offering-level availability owner.
func New(config Config, clock Clock, publisher Publisher) (*Tracker, error) {
	if config.FailureThreshold <= 0 || config.OpenDuration <= 0 {
		return nil, ErrInvalidConfig
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Tracker{
		config:    config,
		clock:     clock,
		publisher: publisher,
		records:   make(map[Offering]*entry),
	}, nil
}

// OfferingFromRoute converts a planned route to runtime identity.
func OfferingFromRoute(route routing.Route) Offering {
	return Offering{ProviderID: route.ProviderID, ProviderModelID: route.ProviderModelID}
}

// Acquire admits one attempt. A half-open offering admits one probe at a time.
func (t *Tracker) Acquire(route routing.Route) bool {
	if t == nil {
		return true
	}
	offering := OfferingFromRoute(route)
	if validateOffering(offering) != nil {
		return false
	}

	t.mu.Lock()
	entry, exists := t.records[offering]
	if !exists || entry.state == StateHealthy {
		t.mu.Unlock()
		return true
	}

	changed := t.refreshEntryLocked(entry, t.clock.Now())
	admitted := false
	if entry.state == StateHalfOpen && !entry.probeInFlight {
		entry.probeInFlight = true
		admitted = true
	}
	var snapshot Snapshot
	if changed {
		snapshot = t.changedSnapshotLocked()
	}
	t.mu.Unlock()
	if changed {
		t.publish(snapshot)
	}
	return admitted
}

// Release ends a prior half-open admission without recording a provider
// outcome. Credential selection uses it when no provider request ran.
func (t *Tracker) Release(route routing.Route) {
	if t == nil {
		return
	}
	offering := OfferingFromRoute(route)
	if validateOffering(offering) != nil {
		return
	}
	t.mu.Lock()
	if entry := t.records[offering]; entry != nil {
		entry.probeInFlight = false
	}
	t.mu.Unlock()
}

// Refresh makes expired open offerings eligible for one half-open probe.
func (t *Tracker) Refresh(_ context.Context) {
	if t == nil {
		return
	}
	t.mu.Lock()
	now := t.clock.Now()
	changed := false
	for _, entry := range t.records {
		changed = t.refreshEntryLocked(entry, now) || changed
	}
	var snapshot Snapshot
	if changed {
		snapshot = t.changedSnapshotLocked()
	}
	t.mu.Unlock()
	if changed {
		t.publish(snapshot)
	}
}

// RecordSuccess closes the offering circuit and clears failure evidence.
func (t *Tracker) RecordSuccess(route routing.Route, _ time.Duration) {
	if t == nil {
		return
	}
	offering := OfferingFromRoute(route)
	if validateOffering(offering) != nil {
		return
	}

	t.mu.Lock()
	entry, exists := t.records[offering]
	if !exists {
		t.mu.Unlock()
		return
	}
	if entry.state == StateHealthy && entry.consecutiveFailure == 0 && !entry.probeInFlight {
		t.mu.Unlock()
		return
	}
	*entry = entryValue(StateHealthy)
	snapshot := t.changedSnapshotLocked()
	t.mu.Unlock()
	t.publish(snapshot)
}

// RecordFailure applies one normalized failure to the offering state machine.
func (t *Tracker) RecordFailure(route routing.Route, providerFailure *failure.Failure, _ time.Duration) {
	if t == nil || providerFailure == nil || providerFailure.StateScope() != failure.ScopeOffering {
		return
	}
	offering := OfferingFromRoute(route)
	if validateOffering(offering) != nil {
		return
	}

	t.mu.Lock()
	entry := t.records[offering]
	switch providerFailure.Kind() {
	case failure.NotFound, failure.RateLimit, failure.Quota,
		failure.ProviderUnavailable, failure.Unreachable, failure.Timeout:
		if entry == nil {
			value := entryValue(StateHealthy)
			entry = &value
			t.records[offering] = entry
		}
	default:
		if entry != nil {
			entry.probeInFlight = false
		}
		t.mu.Unlock()
		return
	}

	changed := false
	switch providerFailure.Kind() {
	case failure.NotFound:
		*entry = entryValue(StateUnavailable)
		entry.failureKind = failure.NotFound
		changed = true
	case failure.RateLimit, failure.Quota, failure.ProviderUnavailable,
		failure.Unreachable, failure.Timeout:
		entry.failureKind = providerFailure.Kind()
		entry.consecutiveFailure++
		entry.probeInFlight = false
		changed = true
		if entry.state == StateHalfOpen || entry.consecutiveFailure >= t.config.FailureThreshold {
			entry.state = StateOpen
			entry.openUntil = t.clock.Now().Add(t.config.OpenDuration)
		}
	}
	var snapshot Snapshot
	if changed {
		snapshot = t.changedSnapshotLocked()
	}
	t.mu.Unlock()
	if changed {
		t.publish(snapshot)
	}
}

// Reset makes one offering healthy. Catalog activation or an operator action can use it.
func (t *Tracker) Reset(offering Offering) error {
	if err := validateOffering(offering); err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if _, exists := t.records[offering]; !exists {
		t.mu.Unlock()
		return nil
	}
	delete(t.records, offering)
	snapshot := t.changedSnapshotLocked()
	t.mu.Unlock()
	t.publish(snapshot)
	return nil
}

// Snapshot returns a caller-owned immutable state generation.
func (t *Tracker) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

// LastPublishError returns the latest derived-projection publication error.
func (t *Tracker) LastPublishError() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastError
}

func (t *Tracker) refreshEntryLocked(entry *entry, now time.Time) bool {
	if entry.state != StateOpen || now.Before(entry.openUntil) {
		return false
	}
	entry.state = StateHalfOpen
	entry.openUntil = time.Time{}
	entry.probeInFlight = false
	return true
}

func (t *Tracker) changedSnapshotLocked() Snapshot {
	t.revision++
	return t.snapshotLocked()
}

func (t *Tracker) snapshotLocked() Snapshot {
	records := make([]Record, 0, len(t.records))
	for offering, entry := range t.records {
		records = append(records, Record{
			Offering:           offering,
			State:              entry.state,
			FailureKind:        entry.failureKind,
			ConsecutiveFailure: entry.consecutiveFailure,
			OpenUntil:          entry.openUntil,
		})
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].Offering.ProviderID != records[right].Offering.ProviderID {
			return records[left].Offering.ProviderID < records[right].Offering.ProviderID
		}
		return records[left].Offering.ProviderModelID < records[right].Offering.ProviderModelID
	})
	return Snapshot{Revision: t.revision, Records: records}
}

func (t *Tracker) publish(snapshot Snapshot) {
	if t.publisher == nil {
		return
	}
	t.publishMu.Lock()
	defer t.publishMu.Unlock()
	if snapshot.Revision <= t.publishedRevision {
		return
	}
	err := t.publisher.PublishAvailability(snapshot)
	t.mu.Lock()
	t.lastError = err
	t.mu.Unlock()
	if err == nil {
		t.publishedRevision = snapshot.Revision
	}
}

func entryValue(state State) entry {
	return entry{state: state}
}

func validateOffering(offering Offering) error {
	if strings.TrimSpace(offering.ProviderID) == "" || strings.TrimSpace(offering.ProviderModelID) == "" {
		return ErrInvalidOffering
	}
	return nil
}
