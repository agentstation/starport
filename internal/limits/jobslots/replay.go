package jobslots

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"math"
	"slices"

	"github.com/agentstation/starport/internal/storage"
)

const replayClaimLimit = 64

// RecoveryReader reads immutable pre-step records with their original expiry.
// Exact retries require the same snapshot, even after the target changes.
type RecoveryReader interface {
	ReadCaptured(context.Context, string, int) (storage.TransferRecord, error)
}

// RecoveryEnumerator visits a complete immutable view in strict storage-key order.
// Account establishment requires this census as well as bounded record reads.
type RecoveryEnumerator interface {
	Enumerate(context.Context, func(storage.TransferRecord) error) error
}

// PrepareClaimReplay prepares later ownership under existing account history.
// The coordinator combines linked work changes, validates the closed final view,
// and proves independent interval coverage before activation. These writes grant no permission.
func PrepareClaimReplay(ctx context.Context, source RecoveryReader, later []Claim) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || len(later) == 0 || len(later) > replayClaimLimit {
		return nil, ErrInvalid
	}
	mutations := map[string]storage.CompareAndSwapMutation{}
	counts := map[string]counter{}
	oldHeld := map[string]int64{}
	nextHeld := map[string]int64{}
	for _, after := range later {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := replayClaimBytes(after)
		if err != nil {
			return nil, err
		}
		key := claimKey(after.Account, after.ID)
		if _, ok := mutations[key]; ok {
			return nil, ErrInvalid
		}
		if _, ok := counts[after.Account]; !ok {
			total, history, err := readReplayAccount(ctx, source, after.Account)
			if err != nil {
				return nil, err
			}
			item, err := verifyReplayRecord(total)
			if err != nil {
				return nil, err
			}
			counts[after.Account] = counter{Version: recordVersion, Total: *item.Total}
			mutations[total.Key] = storage.CompareAndSwapMutation{Key: total.Key, ExpectedValue: total.Value}
			mutations[history.Key] = storage.CompareAndSwapMutation{Key: history.Key, ExpectedValue: history.Value, NewValue: history.Value}
			marker, err := readReplayRecord(ctx, source, replayAccountKey(after.Account))
			if err != nil || marker != nil {
				return nil, errors.Join(ErrHistoryUnknown, err)
			}
			// The absent marker guard excludes concurrent account staging.
			mutations[replayAccountKey(after.Account)] = storage.CompareAndSwapMutation{Key: replayAccountKey(after.Account)}
		}
		before, err := readReplayRecord(ctx, source, key)
		if err != nil {
			return nil, err
		}
		heldBefore := int64(0)
		if before != nil {
			item, err := verifyReplayRecord(*before)
			if err != nil || item.Claim == nil {
				return nil, errors.Join(ErrHistoryUnknown, err)
			}
			if err := verifyClaimProgress(*item.Claim, after); err != nil {
				return nil, err
			}
			if !item.Claim.Released {
				heldBefore = 1
			}
		}
		heldAfter := int64(0)
		if !after.Released {
			heldAfter = 1
		}
		oldHeld[after.Account] += heldBefore
		nextHeld[after.Account] += heldAfter
		mutation := storage.CompareAndSwapMutation{Key: key, NewValue: data}
		if before != nil {
			mutation.ExpectedValue = before.Value
		}
		mutations[key] = mutation
	}
	for account, count := range counts {
		if count.Total < oldHeld[account] || nextHeld[account] > math.MaxInt64-(count.Total-oldHeld[account]) {
			return nil, ErrHistoryUnknown
		}
		count.Total = count.Total - oldHeld[account] + nextHeld[account]
		data, err := json.Marshal(count)
		if err != nil {
			return nil, err
		}
		mutation := mutations[countKey(account)]
		mutation.NewValue = data
		mutations[mutation.Key] = mutation
	}
	return orderedReplayMutations(mutations)
}

func verifyClaimProgress(before, after Claim) error {
	if before.Version != after.Version || before.Account != after.Account || before.ID != after.ID || before.JobID != after.JobID || before.Kind != after.Kind || before.Bound != after.Bound || !before.CreatedAt.Equal(after.CreatedAt) {
		return ErrClaimConflict
	}
	if before.Attached && !after.Attached || before.Released && (!after.Released || before.Attached != after.Attached) {
		return ErrClaimConflict
	}
	return nil
}

func replayClaimBytes(claim Claim) ([]byte, error) {
	if !claim.valid() {
		return nil, ErrInvalid
	}
	// Normalize time locations so equivalent input produces the same receipt bytes.
	claim.CreatedAt = claim.CreatedAt.UTC()
	data, err := json.Marshal(claim, json.Deterministic(true))
	if err != nil || len(data) > maxRecordBytes {
		return nil, errors.Join(ErrInvalid, err)
	}
	return data, nil
}

func readReplayRecord(ctx context.Context, source RecoveryReader, key string) (*storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record, err := source.ReadCaptured(ctx, key, maxRecordBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.Key != key || record.ExpiresAtMillis != 0 || len(record.Value) == 0 || len(record.Value) > maxRecordBytes {
		return nil, ErrHistoryUnknown
	}
	record.Value = bytes.Clone(record.Value)
	return &record, nil
}

func readReplayAccount(ctx context.Context, source RecoveryReader, account string) (storage.TransferRecord, storage.TransferRecord, error) {
	total, err := readReplayRecord(ctx, source, countKey(account))
	if err != nil || total == nil {
		return storage.TransferRecord{}, storage.TransferRecord{}, errors.Join(ErrHistoryUnknown, err)
	}
	history, err := readReplayRecord(ctx, source, historyKey(account))
	if err != nil || history == nil {
		return storage.TransferRecord{}, storage.TransferRecord{}, errors.Join(ErrHistoryUnknown, err)
	}
	for _, record := range []*storage.TransferRecord{total, history} {
		if _, err := verifyReplayRecord(*record); err != nil {
			return storage.TransferRecord{}, storage.TransferRecord{}, err
		}
	}
	return *total, *history, nil
}

func orderedReplayMutations(mutations map[string]storage.CompareAndSwapMutation) ([]storage.CompareAndSwapMutation, error) {
	if len(mutations) > 128 {
		return nil, ErrInvalid
	}
	keys := make([]string, 0, len(mutations))
	for key := range mutations {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]storage.CompareAndSwapMutation, 0, len(keys))
	for _, key := range keys {
		result = append(result, mutations[key])
	}
	return result, nil
}

// Replay must not discard fields that this owner version cannot interpret.
func verifyReplayRecord(record storage.TransferRecord) (RecoveryRecord, error) {
	item, err := VerifyRecoveryRecord(record)
	if err != nil {
		return item, err
	}
	if item.Claim != nil {
		var claim Claim
		if json.Unmarshal(record.Value, &claim, json.RejectUnknownMembers(true)) != nil {
			return RecoveryRecord{}, ErrHistoryUnknown
		}
	}
	if item.Total != nil {
		var count counter
		if json.Unmarshal(record.Value, &count, json.RejectUnknownMembers(true)) != nil {
			return RecoveryRecord{}, ErrHistoryUnknown
		}
	}
	return item, nil
}
