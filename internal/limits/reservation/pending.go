package reservation

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/storage"
)

// RetainEvidence preserves measured usage after a failed settlement. It changes
// no balances. The reservation continues to deduct its full capacity until the
// original settlement succeeds. A storage outage can prevent this write too.
func (r *Repository) RetainEvidence(ctx context.Context, id string, evidence Evidence) error {
	if !validID(evidence.ID) || evidence.Tokens < 0 {
		return ErrInvalid
	}
	for range maxConflicts {
		record, previous, err := r.readRecord(ctx, id)
		if err != nil {
			return err
		}
		if record.State == Settled {
			if sameEvidence(record.Evidence, &evidence) {
				return nil
			}
			return ErrIdentityConflict
		}
		if record.State != Dispatched && record.State != Uncertain {
			return ErrTransition
		}
		if record.Pending != nil {
			if sameEvidence(record.Pending, &evidence) {
				return nil
			}
			return ErrIdentityConflict
		}
		if record.Unresolved != nil {
			if sameEvidence(record.Unresolved, &evidence) {
				return nil
			}
			return ErrIdentityConflict
		}
		_, err = record.Attempt.amount(evidence.Quantities)
		if err != nil && !errors.Is(err, ErrOverflow) {
			return err
		}
		record.State, record.Pending, record.Reason = Uncertain, &evidence, "required_settlement_failed"
		mutation, err := encodeMutation(storageKey("attempt", id), previous, record)
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

// ReconcileRetained resumes settlement from durable evidence after restart.
// No evidence means that provider or operator reconciliation is still required.
func (r *Repository) ReconcileRetained(ctx context.Context, id string) error {
	record, _, err := r.readRecord(ctx, id)
	if err != nil {
		return err
	}
	if record.State == Settled {
		return nil
	}
	if record.Pending == nil {
		return ErrTransition
	}
	return r.Reconcile(ctx, id, *record.Pending)
}
