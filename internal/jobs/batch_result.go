package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

// BindLineOutput records a stable file identity before the provider can run.
func (r *batchRepository) BindLineOutput(ctx context.Context, claim BatchLine, file ResultFile) (BatchLine, error) {
	if file.ID == "" || file.ExpiresAt.IsZero() {
		return BatchLine{}, ErrInvalidBatch
	}
	return r.mutateLine(ctx, claim, func(line *BatchLine) error {
		if line.OutputFileID != "" {
			if line.OutputFileID != file.ID || !line.OutputExpiresAt.Equal(file.ExpiresAt) {
				return ErrInvalidBatch
			}
			return nil
		}
		line.OutputFileID = file.ID
		line.OutputExpiresAt = file.ExpiresAt
		return nil
	})
}

// RecordLineResult pins the first observed result before its byte write.
// A retained digest does not establish that the output bytes are available.
func (r *batchRepository) RecordLineResult(ctx context.Context, claim BatchLine, digest string, size int64, failed bool) (BatchLine, error) {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size || size < 0 {
		return BatchLine{}, ErrInvalidBatch
	}
	return r.mutateLine(ctx, claim, func(line *BatchLine) error {
		if line.OutputFileID == "" || line.OutputFileID != claim.OutputFileID {
			return ErrInvalidBatch
		}
		if line.ResultDigest != "" {
			if line.ResultDigest != digest || line.ResultBytes != size || line.ResultFailed != failed {
				return ErrInvalidBatch
			}
			return nil
		}
		line.ResultDigest = digest
		line.ResultBytes = size
		line.ResultFailed = failed
		return nil
	})
}

// ConfirmLineResult marks output durable after the file owner accepts its bytes.
func (r *batchRepository) ConfirmLineResult(ctx context.Context, claim BatchLine) (BatchLine, error) {
	return r.mutateLine(ctx, claim, func(line *BatchLine) error {
		if line.ResultDigest == "" || line.ResultDigest != claim.ResultDigest || line.ResultBytes != claim.ResultBytes || line.ResultFailed != claim.ResultFailed || line.OutputFileID != claim.OutputFileID {
			return ErrInvalidBatch
		}
		line.ResultReady = true
		return nil
	})
}

func (r *batchRepository) mutateLine(ctx context.Context, claim BatchLine, change func(*BatchLine) error) (BatchLine, error) {
	key := batchLineKey(claim.Account, claim.BatchID, claim.Number)
	for range 8 {
		data, err := r.store.GetBounded(ctx, key, 8192)
		if err != nil {
			return BatchLine{}, err
		}
		var line BatchLine
		if json.Unmarshal(data, &line) != nil || !line.valid() {
			return BatchLine{}, ErrCorruptBatchRecord
		}
		if line.Account != claim.Account || line.BatchID != claim.BatchID || line.Number != claim.Number || line.InputDigest != claim.InputDigest || line.RequestID != claim.RequestID {
			return BatchLine{}, ErrInvalidBatch
		}
		if err := change(&line); err != nil {
			return BatchLine{}, err
		}
		if !line.valid() {
			return BatchLine{}, ErrInvalidBatch
		}
		next, err := json.Marshal(line)
		if err != nil {
			return BatchLine{}, err
		}
		if bytes.Equal(data, next) {
			return line, nil
		}
		err = r.store.CompareAndSwap(ctx, key, data, next)
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

func (s *BatchService) retainLineResult(ctx context.Context, files BatchIO, claim BatchLine, body []byte, failed bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	digest := sha256.Sum256(body)
	retained, err := s.repository.RecordLineResult(ctx, claim, hex.EncodeToString(digest[:]), int64(len(body)), failed)
	if err != nil {
		return err
	}
	if err := files.StoreResult(ctx, retained.OutputFileID, retained.ResultBytes, retained.ResultDigest, bytes.NewReader(body)); err != nil {
		return err
	}
	_, err = s.repository.ConfirmLineResult(ctx, retained)
	return err
}
