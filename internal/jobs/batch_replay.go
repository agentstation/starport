package jobs

import (
	"context"
	"encoding/json/v2"
	"errors"
	"maps"
	"slices"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

// BatchReplay describes one bounded step against an immutable captured source.
// Lines can stage before Publish, which requires complete claimed-line coverage.
// Every step requires the native import barrier. No step grants execution permission.
type BatchReplay struct {
	Batch   Batch
	Lines   []BatchLine
	Publish bool
}

type batchReplayView struct {
	source  reservation.BackupReader
	records map[string]storage.TransferRecord
}

func (v batchReplayView) ReadCaptured(ctx context.Context, key string, maximum int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	if record, ok := v.records[key]; ok {
		if len(record.Value) > maximum {
			return storage.TransferRecord{}, storage.ErrValueTooLarge
		}
		return record, nil
	}
	return v.source.ReadCaptured(ctx, key, maximum)
}

func (v batchReplayView) GetBounded(ctx context.Context, key string, maximum int) ([]byte, error) {
	record, err := v.ReadCaptured(ctx, key, maximum)
	if err != nil {
		return nil, err
	}
	if record.Key != key || record.ExpiresAtMillis != 0 || len(record.Value) > maximum {
		return nil, ErrCorruptBatchRecord
	}
	return record.Value, nil
}

// PrepareBatchReplay retains later claims and results without running a batch.
// The coordinator must prove independent history coverage and validate linked owner records before activation.
// Each exact retry must use the same immutable pre-step source.
func PrepareBatchReplay(ctx context.Context, source reservation.BackupReader, step BatchReplay) ([]storage.CompareAndSwapMutation, error) {
	if ctx == nil || source == nil || len(step.Lines) > 127 || !step.Publish && len(step.Lines) == 0 {
		return nil, ErrInvalidBatch
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := encodeBatch(step.Batch)
	if err != nil {
		return nil, err
	}
	next, err := decodeBatch(data)
	if err != nil {
		return nil, err
	}
	key := batchStorageKey(next.Account, next.ID)
	view := batchReplayView{source: source, records: map[string]storage.TransferRecord{key: {Key: key, Value: data}}}
	previous, err := source.ReadCaptured(ctx, key, storage.TransferMaxValueBytes)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	if err == nil {
		if previous.Key != key || previous.ExpiresAtMillis != 0 {
			return nil, ErrCorruptBatchRecord
		}
		var retained batchRecord
		if len(previous.Value) > storage.TransferMaxValueBytes || json.Unmarshal(previous.Value, &retained, json.RejectUnknownMembers(true)) != nil {
			return nil, ErrCorruptBatchRecord
		}
		before, err := decodeBatch(previous.Value)
		if err != nil {
			return nil, err
		}
		if err := verifyBatchReplayProgress(before, next); err != nil {
			return nil, err
		}
	} else {
		previous = storage.TransferRecord{Key: key}
	}
	mutations := make(map[string]storage.CompareAndSwapMutation, len(step.Lines)+1)
	for _, line := range step.Lines {
		if err := prepareReplayLine(ctx, view, mutations, next, line); err != nil {
			return nil, err
		}
	}
	for _, line := range step.Lines {
		lineKey := batchLineKey(line.Account, line.BatchID, line.Number)
		if _, _, err := VerifyRecoveryBatchLine(ctx, lineKey, view.records[lineKey].Value, view); err != nil {
			return nil, err
		}
	}
	if step.Publish {
		if _, _, err := VerifyRecoveryBatch(ctx, key, data, view); err != nil {
			return nil, err
		}
		if err := verifyReplayBatchCompletion(ctx, view, next); err != nil {
			return nil, err
		}
		mutations[key] = storage.CompareAndSwapMutation{Key: key, ExpectedValue: previous.Value, NewValue: data}
	}
	result := make([]storage.CompareAndSwapMutation, 0, len(mutations))
	for _, key := range slices.Sorted(maps.Keys(mutations)) {
		result = append(result, mutations[key])
	}
	return result, nil
}

func prepareReplayLine(ctx context.Context, view batchReplayView, mutations map[string]storage.CompareAndSwapMutation, batch Batch, line BatchLine) error {
	if !line.valid() || line.Account != batch.Account || line.BatchID != batch.ID || line.Number > batch.ClaimedLines {
		return ErrInvalidBatch
	}
	key := batchLineKey(line.Account, line.BatchID, line.Number)
	if _, ok := mutations[key]; ok {
		return ErrInvalidBatch
	}
	data, err := json.Marshal(line, json.Deterministic(true))
	if err != nil || len(data) > 8192 {
		return errors.Join(ErrInvalidBatch, err)
	}
	previous, err := view.source.ReadCaptured(ctx, key, 8192)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if err == nil {
		var before BatchLine
		if previous.Key != key || previous.ExpiresAtMillis != 0 || len(previous.Value) > 8192 || json.Unmarshal(previous.Value, &before, json.RejectUnknownMembers(true)) != nil || !before.valid() {
			return ErrCorruptBatchRecord
		}
		if err := verifyReplayLineProgress(before, line); err != nil {
			return err
		}
	} else {
		previous = storage.TransferRecord{Key: key}
	}
	view.records[key] = storage.TransferRecord{Key: key, Value: data}
	mutations[key] = storage.CompareAndSwapMutation{Key: key, ExpectedValue: previous.Value, NewValue: data}
	return nil
}

func verifyBatchReplayProgress(before, after Batch) error {
	if after.ClaimedLines < before.ClaimedLines || after.CompletedLines < before.CompletedLines || after.FailedLines < before.FailedLines {
		return ErrInvalidBatch
	}
	// Only the claim owner can increase this count. Final publication verifies every line.
	comparable := before
	comparable.ClaimedLines = after.ClaimedLines
	if err := validateBatchReplacement(comparable, after); err != nil {
		return err
	}
	if before.State != after.State && !CanTransition(before.State, after.State) {
		return ErrIllegalTransition
	}
	if before.RunFinished && !after.RunFinished || before.SlotReleased && !after.SlotReleased {
		return ErrInvalidBatch
	}
	if before.State.Terminal() && (!before.TerminalAt.Equal(after.TerminalAt) || before.Reason != after.Reason) {
		return ErrInvalidBatch
	}
	if (before.State.Terminal() || before.RunFinished) && before.ClaimedLines != after.ClaimedLines {
		return ErrInvalidBatch
	}
	return nil
}

func verifyReplayLineProgress(before, after BatchLine) error {
	if before.Account != after.Account || before.BatchID != after.BatchID || before.Number != after.Number || before.RequestID != after.RequestID || before.InputDigest != after.InputDigest {
		return ErrInvalidBatch
	}
	if before.OutputFileID != "" && (before.OutputFileID != after.OutputFileID || !before.OutputExpiresAt.Equal(after.OutputExpiresAt)) {
		return ErrInvalidBatch
	}
	if before.ResultDigest != "" && (before.ResultDigest != after.ResultDigest || before.ResultBytes != after.ResultBytes || before.ResultFailed != after.ResultFailed) {
		return ErrInvalidBatch
	}
	if before.ResultReady && !after.ResultReady {
		return ErrInvalidBatch
	}
	return nil
}

func verifyReplayBatchCompletion(ctx context.Context, view batchReplayView, batch Batch) error {
	completed, failed := 0, 0
	for number := 1; number <= batch.ClaimedLines; number++ {
		key := batchLineKey(batch.Account, batch.ID, number)
		data, err := view.GetBounded(ctx, key, 8192)
		if err != nil {
			return err
		}
		line, _, err := VerifyRecoveryBatchLine(ctx, key, data, view)
		if err != nil {
			return err
		}
		if batch.RunFinished {
			if !line.ResultReady {
				return ErrBatchResultsPending
			}
			if line.ResultFailed {
				failed++
			} else {
				completed++
			}
		}
	}
	if batch.RunFinished && (completed != batch.CompletedLines || failed != batch.FailedLines || (completed > 0) != (batch.OutputFileID != "") || (failed > 0) != (batch.ErrorFileID != "")) {
		return ErrCorruptBatchRecord
	}
	return nil
}
