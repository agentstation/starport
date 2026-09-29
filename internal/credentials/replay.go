package credentials

import (
	"bytes"
	"context"
	"slices"

	"github.com/agentstation/starport/internal/storage"
)

// RecoveryReader reads bounded immutable credential records with their original expiration.
// Replay preparation never changes the source or infers missing credential history.
type RecoveryReader interface {
	ReadCaptured(context.Context, string, int) (storage.TransferRecord, error)
}

// CaptureRecoveryRecord retains encrypted credential evidence without reaching a provider.
func CaptureRecoveryRecord(ctx context.Context, source RecoveryReader, scope, provider string, encryption *EncryptionService) (RecoveryRecord, error) {
	if ctx == nil || source == nil || validateIdentity(scope, provider) != nil {
		return RecoveryRecord{}, ErrRecoveryCredential
	}
	key := StorageKey(scope, provider)
	_, record, err := readReplayRecord(ctx, source, key)
	if err != nil {
		return RecoveryRecord{}, err
	}
	if record == nil {
		return RecoveryRecord{}, ErrNotFound
	}
	if _, err := VerifyRecoveryRecord(ctx, key, record.data, encryption); err != nil {
		return RecoveryRecord{}, err
	}
	return *record, nil
}

// PrepareRecoveryReplay prepares typed credential changes while admission remains closed.
// The source must remain the immutable pre-step snapshot for every exact retry.
// Ciphertext stays encrypted. Grant lists and retained credential configuration remain complete.
func PrepareRecoveryReplay(ctx context.Context, source RecoveryReader, encryption *EncryptionService, changes []RecoveryChange) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || encryption == nil || len(changes) == 0 || len(changes) > 16 {
		return nil, ErrRecoveryCredential
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]storage.CompareAndSwapMutation, 0, len(changes))
	seen := map[string]bool{}
	total := 0
	for _, change := range changes {
		if validateIdentity(change.Scope, change.Provider) != nil {
			return nil, ErrRecoveryCredential
		}
		key := StorageKey(change.Scope, change.Provider)
		if len(key) > storage.TransferMaxKeyBytes {
			return nil, storage.ErrInvalidMutation
		}
		if seen[key] {
			return nil, ErrConflict
		}
		seen[key] = true
		original, before, err := readReplayRecord(ctx, source, key)
		if err != nil {
			return nil, err
		}
		total += len(key) + len(original.Value)
		if change.Next != nil {
			if change.Next.recoveryRecord == nil {
				return nil, ErrRecoveryCredential
			}
			total += len(change.Next.data)
		}
		if total > storage.ImportReplayMaxBytes {
			return nil, storage.ErrValueTooLarge
		}
		if err := verifyReplayExpected(before, change.ExpectedSHA256); err != nil {
			return nil, err
		}
		if before != nil {
			if _, err := VerifyRecoveryRecord(ctx, key, before.data, encryption); err != nil {
				return nil, err
			}
		}
		mutation := storage.CompareAndSwapMutation{Key: key, ExpectedValue: original.Value}
		if change.Next == nil {
			if before == nil {
				return nil, ErrNotFound
			}
		} else {
			next := change.Next
			if _, err := VerifyRecoveryRecord(ctx, key, next.data, encryption); err != nil {
				return nil, err
			}
			if before != nil && (next.stored.Revision < before.stored.Revision || next.stored.Revision == before.stored.Revision && !bytes.Equal(next.data, before.data) || !next.stored.Key.CreatedAt.Equal(before.stored.Key.CreatedAt) || next.stored.Key.UpdatedAt.Before(before.stored.Key.UpdatedAt)) {
				return nil, ErrConflict
			}
			mutation.NewValue = bytes.Clone(next.data)
		}
		result = append(result, mutation)
	}
	slices.SortFunc(result, func(a, b storage.CompareAndSwapMutation) int { return bytes.Compare([]byte(a.Key), []byte(b.Key)) })
	return result, nil
}
