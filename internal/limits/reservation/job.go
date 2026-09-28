package reservation

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/storage"
)

// BindJob assigns an attempt to one job after Begin and before provider dispatch.
// It changes no balances and grants no dispatch permission. An exact retry keeps
// the same binding. Another job cannot reuse it, including after settlement.
func (r *Repository) BindJob(ctx context.Context, id, jobID string) error {
	if !validID(jobID) {
		return ErrInvalid
	}
	for range maxConflicts {
		record, previous, err := r.readRecord(ctx, id)
		if err != nil {
			return err
		}
		if record.JobID != "" {
			if record.JobID == jobID {
				return nil
			}
			return ErrIdentityConflict
		}
		if record.State != Dispatched {
			return ErrTransition
		}
		record.JobID = jobID
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
