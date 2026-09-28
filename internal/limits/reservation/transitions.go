package reservation

import (
	"bytes"
	"context"
	"errors"
	"maps"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

// Begin consumes dispatch permission once. Only a successful return permits a
// provider call. An ambiguous storage response must not trigger dispatch.
func (r *Repository) Begin(ctx context.Context, id string) error {
	for range maxConflicts {
		record, old, err := r.readRecord(ctx, id)
		if err != nil {
			return err
		}
		if record.DisputeID != "" {
			return ErrUnavailable
		}
		if record.State != Reserved {
			return ErrAlreadyDispatched
		}
		record.State = Dispatched
		mutation, err := encodeMutation(storageKey("attempt", id), old, record)
		if err != nil {
			return err
		}
		err = r.store.CompareAndSwapInWindow(ctx, []storage.CompareAndSwapMutation{mutation}, bindingWindow(record.Bindings))
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return ErrUnavailable
}

// MarkUncertain records why capacity remains deducted. It does not expire or refund it.
func (r *Repository) MarkUncertain(ctx context.Context, id, reason string) error {
	if !validID(reason) {
		return ErrInvalid
	}
	for range maxConflicts {
		record, old, err := r.readRecord(ctx, id)
		if err != nil {
			return err
		}
		if record.DisputeID != "" {
			return ErrUnavailable
		}
		if record.State == Uncertain {
			return nil
		}
		if record.State != Dispatched {
			return ErrTransition
		}
		record.State, record.Reason = Uncertain, reason
		mutation, err := encodeMutation(storageKey("attempt", id), old, record)
		if err != nil {
			return err
		}
		err = r.store.CompareAndSwapInWindow(ctx, []storage.CompareAndSwapMutation{mutation}, storage.TimeWindow{})
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return ErrUnavailable
}

// CancelBeforeDispatch releases only capacity whose dispatch permit remains unused.
func (r *Repository) CancelBeforeDispatch(ctx context.Context, id string) error {
	return r.finish(ctx, id, nil)
}

// Reconcile applies reliable usage once under the original prices and windows.
// Different evidence for an already settled identity requires operator repair.
func (r *Repository) Reconcile(ctx context.Context, id string, evidence Evidence) error {
	if !evidence.valid() {
		return ErrInvalid
	}
	return r.finish(ctx, id, &evidence)
}

func (r *Repository) finish(ctx context.Context, id string, evidence *Evidence) error {
attempts:
	for range maxConflicts {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, old, err := r.readRecord(ctx, id)
		if err != nil {
			return err
		}
		amount, overflow, done, err := r.finishRecord(ctx, record, evidence)
		if err != nil || done {
			return err
		}
		keys := make([]string, len(record.Bindings))
		for i, binding := range record.Bindings {
			keys[i] = meterKey(binding.Rule.Meter, binding.Window)
		}
		reads, err := r.snapshot(ctx, keys)
		if err != nil {
			return err
		}
		mutations := make([]storage.CompareAndSwapMutation, 0, len(record.Bindings)+1)
		for _, binding := range record.Bindings {
			state, previous, err := reads.readSettlementWindow(ctx, binding, id, old)
			if errors.Is(err, storage.ErrConflict) {
				continue attempts
			}
			if err != nil {
				return err
			}
			if overflow {
				state.Overflow = true
			} else {
				state.Reserved -= binding.Amount
				actual := amount
				if evidence != nil && binding.Rule.Meter.Dimension == limits.DimensionTokens {
					actual = evidence.Tokens
				}
				consume(state, actual)
			}
			mutation, err := encodeMutation(meterKey(state.Meter, state.Window), previous, state)
			if err != nil {
				return err
			}
			mutations = append(mutations, mutation)
		}
		mutation, err := encodeMutation(storageKey("attempt", id), old, record)
		if err != nil {
			return err
		}
		mutations = append(mutations, mutation)
		err = r.store.CompareAndSwapInWindow(ctx, mutations, storage.TimeWindow{})
		if err == nil && overflow {
			return ErrOverflow
		}
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return ErrUnavailable
}

// readSettlementWindow distinguishes a concurrent settlement from corrupt
// balances. Only a changed attempt permits another settlement read.
func (r *Repository) readSettlementWindow(ctx context.Context, binding Binding, id string, attempt []byte) (*WindowState, []byte, error) {
	state, previous, err := r.readWindow(ctx, binding.Rule.Meter, binding.Window)
	if err != nil || state.Reserved >= binding.Amount {
		return state, previous, err
	}
	_, current, err := r.readRecord(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(attempt, current) {
		return nil, nil, storage.ErrConflict
	}
	return nil, nil, ErrUnavailable
}

// JSON normalizes an absent quantity map to an empty object. Both represent no
// monetary components for token-only evidence and must preserve exact retries.
func sameEvidence(first, second *Evidence) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.NoCharge == second.NoCharge && first.ID == second.ID && first.Tokens == second.Tokens && maps.Equal(first.Quantities, second.Quantities)
}

// finishRecord prepares settlement without writing the record.
// A completed exact retry needs no further window mutation.
func (r *Repository) finishRecord(ctx context.Context, record *Record, evidence *Evidence) (amount int64, overflow, done bool, err error) {
	if record.DisputeID != "" {
		return 0, false, false, ErrUnavailable
	}
	if evidence == nil {
		if record.State == Canceled {
			return 0, false, true, nil
		}
		if record.State != Reserved {
			return 0, false, false, ErrTransition
		}
		record.State, record.NanoUSD = Canceled, record.Attempt.money(0)
	} else {
		if record.Pending != nil && !sameEvidence(record.Pending, evidence) {
			return 0, false, false, ErrIdentityConflict
		}
		if record.Unresolved != nil && !sameEvidence(record.Unresolved, evidence) {
			return 0, false, false, ErrIdentityConflict
		}
		if record.State == Settled {
			if sameEvidence(record.Evidence, evidence) {
				return 0, false, true, nil
			}
			return 0, false, false, ErrIdentityConflict
		}
		if record.State != Dispatched && record.State != Uncertain {
			return 0, false, false, ErrTransition
		}
		amount, err = record.Attempt.evidenceAmount(evidence)
		overflow = errors.Is(err, ErrOverflow)
		if err != nil && !overflow {
			return 0, false, false, err
		}
		if overflow {
			if sameEvidence(record.Unresolved, evidence) {
				return 0, false, false, ErrOverflow
			}
			record.State, record.Unresolved, record.Reason = Uncertain, evidence, "valuation_overflow"
			record.Pending = nil
		} else {
			at, clockErr := r.store.AuthorityTime(ctx)
			if clockErr != nil {
				return 0, false, false, clockErr
			}
			if at.IsZero() || at.Before(record.AdmittedAt) {
				return 0, false, false, ErrUnavailable
			}
			record.SettledAt = at
			record.State, record.Evidence, record.NanoUSD, record.Reason = Settled, evidence, record.Attempt.money(amount), ""
			if evidence.NoCharge {
				record.NanoUSD = &amount
			}
			record.Unresolved = nil
			record.Pending = nil
		}
	}
	return amount, overflow, false, nil
}
