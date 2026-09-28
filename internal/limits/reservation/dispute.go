package reservation

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

// FlagDispute preserves accepted evidence and blocks new admission in its original windows.
// It records no replacement charge and grants no new capacity.
func (r *Repository) FlagDispute(ctx context.Context, id, evidenceID string) error {
	return r.flagDispute(ctx, id, evidenceID, nil)
}

// FlagDisputeWith publishes external evidence and its budget restriction atomically.
// The evidence owner supplies a persistent replacement on the same storage authority.
// A stale replacement returns storage.ErrConflict without changing any budget.
func (r *Repository) FlagDisputeWith(ctx context.Context, id, evidenceID string, mutation storage.CompareAndSwapMutation) error {
	if mutation.Key == "" || strings.HasPrefix(mutation.Key, "budget:v1:") || len(mutation.ExpectedValue) == 0 || len(mutation.NewValue) == 0 || mutation.TTL != 0 {
		return ErrInvalid
	}
	return r.flagDispute(ctx, id, evidenceID, &mutation)
}

func (r *Repository) flagDispute(ctx context.Context, id, evidenceID string, attachment *storage.CompareAndSwapMutation) error {
	if !validID(evidenceID) {
		return ErrInvalid
	}
	for range maxConflicts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attachment != nil {
			current, lifetime, err := r.store.ReadWithLifetime(ctx, attachment.Key, len(attachment.ExpectedValue))
			if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrValueTooLarge) {
				return storage.ErrConflict
			}
			if err != nil {
				return err
			}
			if lifetime != 0 || !bytes.Equal(current, attachment.ExpectedValue) {
				return storage.ErrConflict
			}
		}
		record, old, err := r.readRecord(ctx, id)
		if err != nil {
			return err
		}
		if record.ResolvedDisputeID == evidenceID {
			if attachment != nil {
				return ErrIdentityConflict
			}
			return nil
		}
		if record.DisputeID != "" && record.DisputeID != evidenceID {
			return ErrIdentityConflict
		}
		if record.DisputeID == evidenceID && attachment == nil {
			return nil
		}
		if record.State != Dispatched && record.State != Uncertain && record.State != Settled {
			return ErrTransition
		}
		mutations := make([]storage.CompareAndSwapMutation, 0, len(record.Bindings)+1)
		if record.DisputeID == "" {
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
		}

		record.DisputeID = evidenceID
		mutation, err := encodeMutation(storageKey("attempt", id), old, record)
		if err != nil {
			return err
		}
		mutations = append(mutations, mutation)
		if attachment != nil {
			mutations = append(mutations, *attachment)
		}
		err = r.store.CompareAndSwapInWindow(ctx, mutations, storage.TimeWindow{})
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return ErrUnavailable
}
