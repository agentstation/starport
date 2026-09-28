package reservation

import (
	"context"
	"errors"
	"math"

	"github.com/agentstation/starport/internal/storage"
)

// FlagDispute preserves accepted evidence and blocks new admission in its original windows.
// It records no replacement charge and grants no new capacity.
func (r *Repository) FlagDispute(ctx context.Context, id, evidenceID string) error {
	if !validID(evidenceID) {
		return ErrInvalid
	}
	for range maxConflicts {
		record, old, err := r.readRecord(ctx, id)
		if err != nil {
			return err
		}
		if record.ResolvedDisputeID == evidenceID {
			return nil
		}
		if record.DisputeID != "" {
			if record.DisputeID == evidenceID {
				return nil
			}
			return ErrIdentityConflict
		}
		if record.State != Dispatched && record.State != Uncertain && record.State != Settled {
			return ErrTransition
		}
		mutations := make([]storage.CompareAndSwapMutation, 0, len(record.Bindings)+1)
		for _, binding := range record.Bindings {
			window, previous, err := r.readWindow(ctx, binding.Rule.Meter, binding.Window)
			if err != nil {
				return err
			}
			if window.ActiveDisputes == math.MaxInt64 {
				return ErrOverflow
			}
			window.ActiveDisputes++
			window.ReconciliationRequired = true
			mutation, err := encodeMutation(meterKey(binding.Rule.Meter, binding.Window), previous, window)
			if err != nil {
				return err
			}
			mutations = append(mutations, mutation)
		}
		record.DisputeID = evidenceID
		mutation, err := encodeMutation(storageKey("attempt", id), old, record)
		if err != nil {
			return err
		}
		mutations = append(mutations, mutation)
		err = r.store.CompareAndSwapInWindow(ctx, mutations, storage.TimeWindow{})
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return ErrUnavailable
}
