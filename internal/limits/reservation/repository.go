package reservation

import (
	"context"
	"encoding/json/v2"
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

const (
	recordVersion = 1
	maxRecordSize = 64 << 10
	maxConflicts  = 64
)

// Repository owns reservations over an approved storage authority.
// Construction reads no balances and starts no background work.
type Repository struct{ store storage.TimeBoundStore }

// Open requires the time and atomic-write contract. A raw shared KV store does
// not satisfy it: the caller must bind its independently approved incarnation.
func Open(store storage.TimeBoundStore) (*Repository, error) {
	if store == nil {
		return nil, ErrUnavailable
	}
	return &Repository{store: store}, nil
}

// EstablishWindow installs verified history only when the meter does not exist.
// The migration or policy owner supplies complete consumption evidence. This
// method cannot infer zero history, replace an active meter, or repair lost state.
func (r *Repository) EstablishWindow(ctx context.Context, meter Meter, at time.Time, consumed int64, proof string) error {
	if !meter.valid() || at.IsZero() || consumed < 0 || !validID(proof) {
		return ErrInvalid
	}
	state := WindowState{Version: recordVersion, Meter: meter, Window: windowFor(meter.Interval, at), HistoryProof: proof, Consumed: consumed}
	key := meterKey(meter, state.Window)
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	err = r.store.CompareAndSwapInWindow(ctx, []storage.CompareAndSwapMutation{{Key: key, NewValue: data}}, storage.TimeWindow{})
	if !errors.Is(err, storage.ErrConflict) {
		return err
	}
	existing, _, err := r.readWindow(ctx, meter, state.Window)
	if err != nil {
		return err
	}
	if existing.HistoryProof != proof {
		return ErrIdentityConflict
	}
	return nil
}

// Reserve deducts all required capacity and records one attempt atomically.
// A matching retry returns the existing record without deducting capacity again.
// Only Begin can grant permission to dispatch a reserved attempt.
func (r *Repository) Reserve(ctx context.Context, attempt Attempt) (*Record, error) {
	if err := validateAttempt(attempt); err != nil {
		return nil, err
	}
	// Detach caller-owned maps and slices before retaining any evidence.
	data, err := json.Marshal(attempt)
	if err != nil || len(data) > maxRecordSize/2 {
		return nil, ErrInvalid
	}
	var owned Attempt
	if err := json.Unmarshal(data, &owned); err != nil {
		return nil, err
	}
	amount, err := owned.Valuation.NanoUSD(owned.Bound)
	if err != nil {
		return nil, err
	}
	for range maxConflicts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		existing, _, err := r.readRecord(ctx, owned.ID)
		if err == nil {
			if !reflect.DeepEqual(existing.Attempt, owned) {
				return nil, ErrIdentityConflict
			}
			return existing, nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		now, err := r.store.AuthorityTime(ctx)
		if err != nil {
			return nil, err
		}
		record := &Record{Version: recordVersion, Attempt: owned, State: Reserved, AdmittedAt: now, NanoUSD: amount}
		mutations := make([]storage.CompareAndSwapMutation, 0, len(owned.Rules)+1)
		for _, rule := range owned.Rules {
			window := windowFor(rule.Meter.Interval, now)
			state, old, err := r.readWindow(ctx, rule.Meter, window)
			if err != nil {
				return nil, err
			}
			bound := amount
			if rule.Meter.Dimension == limits.DimensionTokens {
				bound = owned.TokenBound
			}
			if state.Overflow || state.Consumed > rule.Limit || state.Reserved > rule.Limit-state.Consumed || bound > rule.Limit-state.Consumed-state.Reserved {
				return nil, ErrExhausted
			}
			state.Reserved += bound
			mutation, err := encodeMutation(meterKey(rule.Meter, window), old, state)
			if err != nil {
				return nil, err
			}
			mutations = append(mutations, mutation)
			record.Bindings = append(record.Bindings, Binding{Rule: rule, Window: window, Amount: bound})
		}
		mutation, err := encodeMutation(storageKey("attempt", owned.ID), nil, record)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, mutation)
		err = r.store.CompareAndSwapInWindow(ctx, mutations, bindingWindow(record.Bindings))
		if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrTimeWindowChanged) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return record, nil
	}
	return nil, ErrUnavailable
}

// Inspect reads durable state for recovery and diagnostics without changing it.
func (r *Repository) Inspect(ctx context.Context, id string) (*Record, error) {
	value, _, err := r.readRecord(ctx, id)
	return value, err
}

// Window reads a verified aggregate. Missing history is an error, never zero.
func (r *Repository) Window(ctx context.Context, meter Meter, at time.Time) (*WindowState, error) {
	if !meter.valid() || at.IsZero() {
		return nil, ErrInvalid
	}
	value, _, err := r.readWindow(ctx, meter, windowFor(meter.Interval, at))
	return value, err
}

func (r *Repository) readRecord(ctx context.Context, id string) (*Record, []byte, error) {
	if !validID(id) {
		return nil, nil, ErrInvalid
	}
	data, err := r.read(ctx, storageKey("attempt", id))
	if err != nil {
		return nil, nil, err
	}
	var record Record
	if json.Unmarshal(data, &record) != nil || record.Version != recordVersion || record.Attempt.ID != id || validateAttempt(record.Attempt) != nil || record.AdmittedAt.IsZero() || len(record.Bindings) != len(record.Attempt.Rules) {
		return nil, nil, ErrUnavailable
	}
	if err := validateBindings(&record); err != nil {
		return nil, nil, err
	}
	return &record, data, nil
}

func (r *Repository) readWindow(ctx context.Context, meter Meter, window storage.TimeWindow) (*WindowState, []byte, error) {
	data, err := r.read(ctx, meterKey(meter, window))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil, ErrHistoryUnknown
	}
	if err != nil {
		return nil, nil, err
	}
	var state WindowState
	if json.Unmarshal(data, &state) != nil || state.Version != recordVersion || state.Meter != meter || state.Window != window || !validID(state.HistoryProof) || state.Consumed < 0 || state.Reserved < 0 {
		return nil, nil, ErrUnavailable
	}
	return &state, data, nil
}

func (r *Repository) read(ctx context.Context, key string) ([]byte, error) {
	value, lifetime, err := r.store.ReadWithLifetime(ctx, key, maxRecordSize)
	if err != nil {
		return nil, err
	}
	if lifetime != 0 || len(value) == 0 {
		return nil, ErrUnavailable
	}
	return value, nil
}

func encodeMutation(key string, expected []byte, value any) (storage.CompareAndSwapMutation, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if len(data) > maxRecordSize {
		return storage.CompareAndSwapMutation{}, ErrInvalid
	}
	return storage.CompareAndSwapMutation{Key: key, ExpectedValue: expected, NewValue: data}, nil
}

func bindingWindow(bindings []Binding) storage.TimeWindow {
	var result storage.TimeWindow
	for _, binding := range bindings {
		if result.Start.IsZero() || binding.Window.Start.After(result.Start) {
			result.Start = binding.Window.Start
		}
		if result.End.IsZero() || binding.Window.End.Before(result.End) {
			result.End = binding.Window.End
		}
	}
	return result
}

func validateAttempt(attempt Attempt) error {
	for _, value := range []string{attempt.ID, attempt.RequestID, attempt.AccountID, attempt.KeyID, attempt.OfferingID, attempt.CatalogGeneration, attempt.Operation} {
		if !validID(value) {
			return ErrInvalid
		}
	}
	if attempt.TeamID != "" && !validID(attempt.TeamID) || attempt.TokenBound < 0 || len(attempt.Rules) == 0 || len(attempt.Rules) > 6 {
		return ErrInvalid
	}
	seen := make(map[Meter]bool, len(attempt.Rules))
	for _, rule := range attempt.Rules {
		holder := attempt.AccountID
		switch rule.Meter.Scope {
		case limits.ScopeKey:
			holder = attempt.KeyID
		case limits.ScopeTeam:
			holder = attempt.TeamID
		}
		if !rule.Meter.valid() || holder != rule.Meter.Holder || seen[rule.Meter] || rule.Limit <= 0 || !validID(rule.PolicyRevision) {
			return ErrInvalid
		}
		seen[rule.Meter] = true
	}
	_, err := attempt.Valuation.NanoUSD(attempt.Bound)
	return err
}

func consume(state *WindowState, amount int64) {
	if amount > math.MaxInt64-state.Consumed {
		state.Consumed, state.Overflow = math.MaxInt64, true
		return
	}
	state.Consumed += amount
}
