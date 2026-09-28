package jobs

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/google/uuid"
)

var (
	// ErrBatchLineClaimed prevents a retained line claim from running again.
	ErrBatchLineClaimed = errors.New("jobs: batch line already claimed")
	// ErrBatchLineNotFound reports a missing account-owned line record.
	ErrBatchLineNotFound = errors.New("jobs: batch line not found")
)

// BatchLine binds one invocation to its immutable input and request identity.
// A claim can represent uncertain work. Its presence never authorizes a replay.
type BatchLine struct {
	OutputFileID    string    `json:"output_file_id,omitempty"`
	OutputExpiresAt time.Time `json:"output_expires_at,omitzero"`
	ResultDigest    string    `json:"result_digest,omitempty"`
	ResultBytes     int64     `json:"result_bytes,omitzero"`
	ResultFailed    bool      `json:"result_failed,omitzero"`
	ResultReady     bool      `json:"result_ready,omitzero"`
	Version         int       `json:"version"`
	Account         string    `json:"account"`
	BatchID         string    `json:"batch_id"`
	Number          int       `json:"number"`
	InputDigest     string    `json:"input_digest"`
	RequestID       string    `json:"request_id"`
}

func (line BatchLine) valid() bool {
	digest, err := hex.DecodeString(line.InputDigest)
	if (line.OutputFileID == "") != line.OutputExpiresAt.IsZero() || line.ResultBytes < 0 {
		return false
	}
	if line.ResultDigest != "" {
		result, err := hex.DecodeString(line.ResultDigest)
		if err != nil || len(result) != 32 || line.OutputFileID == "" {
			return false
		}
	} else if line.ResultReady || line.ResultFailed || line.ResultBytes != 0 {
		return false
	}
	return line.Version == 2 && line.Account != "" && line.BatchID != "" && line.Number > 0 && line.RequestID != "" && err == nil && len(digest) == 32
}

// ClaimLine atomically records one invocation against the current batch state.
// Only a successful return permits execution. A lost acknowledgment cannot retry it.
func (r *batchRepository) ClaimLine(ctx context.Context, account, id string, number int, digest string) (BatchLine, error) {
	line := BatchLine{Version: 2, Account: account, BatchID: id, Number: number, InputDigest: digest, RequestID: uuid.NewString()}
	if !line.valid() {
		return BatchLine{}, ErrInvalidBatch
	}
	claim, err := json.Marshal(line)
	if err != nil {
		return BatchLine{}, err
	}
	key := batchStorageKey(account, id)
	for range batchReplaceAttempts {
		data, err := r.store.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			return BatchLine{}, ErrBatchNotFound
		}
		if err != nil {
			return BatchLine{}, err
		}
		batch, err := decodeBatch(data)
		if err != nil {
			return BatchLine{}, err
		}
		if batch.Account != account || batch.ID != id {
			return BatchLine{}, ErrCorruptBatchRecord
		}
		if number <= batch.ClaimedLines {
			return BatchLine{}, ErrBatchLineClaimed
		}
		if batch.State.Terminal() {
			return BatchLine{}, ErrBatchAlreadyEnded
		}
		if batch.State != JobStateRunning || number != batch.ClaimedLines+1 || number > batch.TotalLines {
			return BatchLine{}, ErrInvalidBatch
		}
		batch.ClaimedLines = number
		next, err := encodeBatch(batch)
		if err != nil {
			return BatchLine{}, err
		}
		err = r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
			{Key: key, ExpectedValue: data, NewValue: next},
			{Key: batchLineKey(account, id, number), NewValue: claim},
		})
		if errors.Is(err, storage.ErrConflict) {
			continue
		}
		if err != nil {
			return BatchLine{}, err
		}
		return line, nil
	}
	return BatchLine{}, storage.ErrConflict
}

// ReadLine returns retained identity without granting permission to execute.
func (r *batchRepository) ReadLine(ctx context.Context, account, id string, number int) (BatchLine, error) {
	data, err := r.store.GetBounded(ctx, batchLineKey(account, id, number), 8192)
	if errors.Is(err, storage.ErrNotFound) {
		return BatchLine{}, ErrBatchLineNotFound
	}
	if err != nil {
		return BatchLine{}, err
	}
	var line BatchLine
	if err := json.Unmarshal(data, &line); err != nil || !line.valid() || line.Account != account || line.BatchID != id || line.Number != number {
		return BatchLine{}, ErrCorruptBatchRecord
	}
	return line, nil
}

func batchLineKey(account, id string, number int) string {
	return "batch-lines:v1:account:" + base64.RawURLEncoding.EncodeToString([]byte(account)) + ":batch:" + base64.RawURLEncoding.EncodeToString([]byte(id)) + ":line:" + strconv.Itoa(number)
}
