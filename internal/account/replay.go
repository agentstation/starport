package account

import (
	"bytes"
	"context"
	"errors"
	"slices"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

// CaptureRecoveryRecord captures complete account evidence without changing policy.
func CaptureRecoveryRecord(ctx context.Context, source reservation.BackupReader, id string) (RecoveryRecord, error) {
	if ctx == nil || source == nil || ValidateID(id) != nil {
		return RecoveryRecord{}, ErrCorruptRecord
	}
	_, record, err := readReplayRecord(ctx, source, accountStorageKey(id))
	if err != nil {
		return RecoveryRecord{}, err
	}
	if record == nil {
		return RecoveryRecord{}, ErrNotFound
	}
	if record.stored.Account.ID != id {
		return RecoveryRecord{}, ErrCorruptRecord
	}
	return *record, nil
}

// PrepareRecoveryReplay prepares bounded account changes under closed recovery authority.
// The source must remain the immutable pre-step snapshot for exact retries.
// It preserves budget history IDs and never initializes a budget window or fresh-history grant.
func PrepareRecoveryReplay(ctx context.Context, source reservation.BackupReader, changes []RecoveryChange) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || len(changes) == 0 || len(changes) > 16 {
		return nil, ErrCorruptRecord
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mutations := map[string]storage.CompareAndSwapMutation{}
	for _, change := range changes {
		if ValidateID(change.ID) != nil {
			return nil, ErrCorruptRecord
		}
		key := accountStorageKey(change.ID)
		if _, exists := mutations[key]; exists {
			return nil, ErrConflict
		}
		original, before, err := readReplayRecord(ctx, source, key)
		if err != nil {
			return nil, err
		}
		if err := verifyReplayExpected(before, change.ExpectedSHA256); err != nil {
			return nil, err
		}
		if before != nil && before.stored.Account.ID != change.ID {
			return nil, ErrCorruptRecord
		}
		if change.Next == nil {
			if before == nil {
				return nil, ErrNotFound
			}
			if change.ID == DefaultID {
				return nil, ErrDefaultImmutable
			}
			mutations[key] = storage.CompareAndSwapMutation{Key: key, ExpectedValue: original.Value}
			continue
		}
		next := change.Next
		if next.stored.Account.ID != change.ID || len(next.data) == 0 {
			return nil, ErrCorruptRecord
		}
		if before != nil {
			if next.stored.Revision < before.stored.Revision || next.stored.Revision == before.stored.Revision && !bytes.Equal(next.data, before.data) || !next.stored.Account.CreatedAt.Equal(before.stored.Account.CreatedAt) || next.stored.Account.UpdatedAt.Before(before.stored.Account.UpdatedAt) {
				return nil, ErrConflict
			}
		}
		holder, err := replayHolder(ctx, source, change.ID)
		if err != nil {
			return nil, err
		}
		mutations[holder.Key] = holder
		mutations[key] = storage.CompareAndSwapMutation{Key: key, ExpectedValue: original.Value, NewValue: bytes.Clone(next.data)}
	}
	keys := make([]string, 0, len(mutations))
	for key := range mutations {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]storage.CompareAndSwapMutation, 0, len(keys))
	total := 0
	for _, key := range keys {
		mutation := mutations[key]
		if len(key) > storage.TransferMaxKeyBytes {
			return nil, storage.ErrInvalidMutation
		}
		total += len(key) + len(mutation.ExpectedValue) + len(mutation.NewValue)
		if total > storage.ImportReplayMaxBytes {
			return nil, storage.ErrValueTooLarge
		}
		result = append(result, mutation)
	}
	return result, nil
}

func replayHolder(ctx context.Context, source reservation.BackupReader, id string) (storage.CompareAndSwapMutation, error) {
	marker, err := reservation.FreshHolderIdentity(limits.ScopeAccount, id)
	if err != nil {
		return marker, err
	}
	prior, err := source.ReadCaptured(ctx, marker.Key, 1024)
	if errors.Is(err, storage.ErrNotFound) {
		return marker, nil
	}
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if prior.Key != marker.Key || prior.ExpiresAtMillis != 0 || !bytes.Equal(prior.Value, marker.NewValue) {
		return storage.CompareAndSwapMutation{}, ErrCorruptRecord
	}
	marker.ExpectedValue = bytes.Clone(prior.Value)
	return marker, nil
}
