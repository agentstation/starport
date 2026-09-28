package jobs

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/storage"
)

// BatchLineStoragePrefix identifies retained execution claims, including uncertain attempts.
const BatchLineStoragePrefix = "batch-lines:v1:account:"

// VerifyRecoveryBatch checks retained line coverage and account-bound file references.
// Deleted files are counted for reconciliation. They never permit another execution.
func VerifyRecoveryBatch(ctx context.Context, key string, data []byte, records files.RecoveryRecordReader) (batch Batch, missingFiles int64, resultErr error) {
	batch, err := decodeBatch(data)
	if err != nil || records == nil || key != batchStorageKey(batch.Account, batch.ID) {
		return Batch{}, 0, ErrCorruptBatchRecord
	}
	for _, reference := range []struct {
		id      string
		purpose files.Purpose
	}{
		{batch.InputFileID, files.PurposeBatch}, {batch.OutputFileID, files.PurposeBatchOutput}, {batch.ErrorFileID, files.PurposeBatchOutput},
	} {
		if reference.id == "" {
			continue
		}
		file, err := files.ReadRecoveryFile(ctx, records, batch.Account, reference.id)
		if errors.Is(err, files.ErrFileNotFound) {
			missingFiles++
			continue
		}
		if err != nil {
			return Batch{}, 0, err
		}
		if file.Purpose != reference.purpose {
			return Batch{}, 0, ErrCorruptBatchRecord
		}
	}
	for index := 0; index < batch.ClaimedLines; index++ {
		number := index + 1
		data, err := records.GetBounded(ctx, batchLineKey(batch.Account, batch.ID, number), 8192)
		if err != nil {
			return Batch{}, 0, errors.Join(ErrCorruptBatchRecord, err)
		}
		var line BatchLine
		if err := json.Unmarshal(data, &line); err != nil || !line.valid() || line.Account != batch.Account || line.BatchID != batch.ID || line.Number != number {
			return Batch{}, 0, ErrCorruptBatchRecord
		}
	}
	return batch, missingFiles, nil
}

// VerifyRecoveryBatchLine preserves each claim and checks its parent and optional output.
// A missing output can follow explicit deletion or interrupted cleanup and remains diagnostic data.
func VerifyRecoveryBatchLine(ctx context.Context, key string, data []byte, records files.RecoveryRecordReader) (line BatchLine, missingFile bool, resultErr error) {
	if records == nil {
		return line, false, ErrCorruptBatchRecord
	}
	if err := json.Unmarshal(data, &line); err != nil || !line.valid() || key != batchLineKey(line.Account, line.BatchID, line.Number) {
		return BatchLine{}, false, ErrCorruptBatchRecord
	}
	parent, err := records.GetBounded(ctx, batchStorageKey(line.Account, line.BatchID), storage.TransferMaxValueBytes)
	if err != nil {
		return BatchLine{}, false, errors.Join(ErrCorruptBatchRecord, err)
	}
	batch, err := decodeBatch(parent)
	if err != nil || batch.Account != line.Account || batch.ID != line.BatchID || line.Number > batch.ClaimedLines {
		return BatchLine{}, false, ErrCorruptBatchRecord
	}
	if line.OutputFileID == "" {
		return line, false, nil
	}
	file, err := files.ReadRecoveryFile(ctx, records, line.Account, line.OutputFileID)
	if errors.Is(err, files.ErrFileNotFound) {
		return line, true, nil
	}
	if err != nil {
		return BatchLine{}, false, err
	}
	if file.Purpose != files.PurposeBatchOutput || !file.ExpiresAt.Equal(line.OutputExpiresAt) {
		return BatchLine{}, false, ErrCorruptBatchRecord
	}
	if line.ResultDigest != "" && !file.RecoveryOutputMatches(line.ResultBytes, line.ResultDigest, line.ResultReady) {
		return BatchLine{}, false, ErrCorruptBatchRecord
	}
	return line, false, nil
}
