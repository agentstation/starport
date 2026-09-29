package apikey

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"math"
	"slices"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/storage"
)

// CaptureRecoveryRecord reads private gateway-key evidence, including its hash and history IDs.
func CaptureRecoveryRecord(ctx context.Context, source reservation.BackupReader, id string) (RecoveryRecord, error) {
	if ctx == nil || source == nil || id == "" {
		return RecoveryRecord{}, ErrCorruptRecord
	}
	_, record, err := readReplayRecord(ctx, source, apiKeyStorageKey(id))
	if err != nil {
		return RecoveryRecord{}, err
	}
	if record == nil {
		return RecoveryRecord{}, ErrNotFound
	}
	if record.stored.APIKey.ID != id {
		return RecoveryRecord{}, ErrCorruptRecord
	}
	return *record, nil
}

// PrepareRecoveryReplay derives bounded key, hash-index, and collection changes.
// The immutable source supplies every expected preimage for exact retries.
// Holder identities survive deletion. No change creates fresh budget-history grants.
func PrepareRecoveryReplay(ctx context.Context, source reservation.BackupReader, request RecoveryReplay) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || len(request.Changes) == 0 || len(request.Changes) > 16 {
		return nil, ErrCorruptRecord
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	indexes, err := readReplayIndexes(ctx, source)
	if err != nil {
		return nil, err
	}
	if request.ExpectedIndexesSHA256 != indexes.digest() {
		return nil, ErrConflict
	}
	count, changed := indexes.value.Count, false
	changes := map[string]storage.CompareAndSwapMutation{}
	for _, change := range request.Changes {
		owner, delta, err := prepareReplayKey(ctx, source, change)
		if err != nil {
			return nil, err
		}
		if indexes.collection == nil && delta != 1 {
			return nil, ErrCorruptRecord
		}
		if delta < 0 {
			if count == 0 {
				return nil, ErrCorruptRecord
			}
			count--
		}
		if delta > 0 {
			if count == math.MaxUint64 {
				return nil, ErrCorruptRecord
			}
			count++
		}
		changed = changed || delta != 0
		for _, mutation := range owner {
			if _, exists := changes[mutation.Key]; exists {
				return nil, ErrConflict
			}
			changes[mutation.Key] = mutation
		}
	}
	metadata, err := replayMetadataMutations(indexes, request.Indexes, count, changed)
	if err != nil {
		return nil, err
	}
	for _, mutation := range metadata {
		changes[mutation.Key] = mutation
	}
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]storage.CompareAndSwapMutation, 0, len(keys))
	total := 0
	for _, key := range keys {
		mutation := changes[key]
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
func prepareReplayKey(ctx context.Context, source reservation.BackupReader, change RecoveryChange) ([]storage.CompareAndSwapMutation, int, error) {
	if change.ID == "" {
		return nil, 0, ErrCorruptRecord
	}
	key := apiKeyStorageKey(change.ID)
	original, before, err := readReplayRecord(ctx, source, key)
	if err != nil {
		return nil, 0, err
	}
	if err := verifyReplayExpected(before, change.ExpectedSHA256); err != nil {
		return nil, 0, err
	}
	if before != nil && before.stored.APIKey.ID != change.ID {
		return nil, 0, ErrCorruptRecord
	}
	mutation := storage.CompareAndSwapMutation{Key: key, ExpectedValue: original.Value}
	delta := 0
	selected := before
	if change.Next == nil {
		if before == nil {
			return nil, 0, ErrNotFound
		}
		delta = -1
	} else {
		selected = change.Next
		if selected.recoveryRecord == nil || len(selected.data) == 0 || selected.stored.APIKey.ID != change.ID {
			return nil, 0, ErrCorruptRecord
		}
		if before != nil {
			if selected.stored.APIKey.Hash != before.stored.APIKey.Hash {
				return nil, 0, ErrHashImmutable
			}
			if selected.stored.Revision < before.stored.Revision || selected.stored.Revision == before.stored.Revision && !bytes.Equal(selected.data, before.data) || !selected.stored.APIKey.CreatedAt.Equal(before.stored.APIKey.CreatedAt) {
				return nil, 0, ErrConflict
			}
		} else {
			delta = 1
		}
		mutation.NewValue = bytes.Clone(selected.data)
	}
	indexKey := hashStorageKey(selected.stored.APIKey.Hash)
	priorIndex, err := readReplayIndex(ctx, source, indexKey)
	if err != nil {
		return nil, 0, err
	}
	if before != nil {
		var index hashRecord
		if json.Unmarshal(priorIndex, &index, json.RejectUnknownMembers(true)) != nil || index.SchemaVersion != StorageSchemaVersion || index.APIKeyID != change.ID {
			return nil, 0, ErrCorruptRecord
		}
	} else if priorIndex != nil {
		return nil, 0, ErrConflict
	}
	index := storage.CompareAndSwapMutation{Key: indexKey, ExpectedValue: priorIndex}
	if change.Next != nil {
		index.NewValue, err = policyrecord.Marshal(hashRecord{SchemaVersion: StorageSchemaVersion, APIKeyID: change.ID})
		if err != nil {
			return nil, 0, err
		}
		if before != nil {
			index.NewValue = bytes.Clone(priorIndex)
		}
	}
	result := []storage.CompareAndSwapMutation{mutation, index}
	if change.Next != nil {
		holder, err := replayHolder(ctx, source, change.ID)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, holder)
	}
	return result, delta, nil
}
func replayHolder(ctx context.Context, source reservation.BackupReader, id string) (storage.CompareAndSwapMutation, error) {
	marker, err := reservation.FreshHolderIdentity(limits.ScopeKey, id)
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
