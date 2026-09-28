package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"time"
)

var ErrBatchResultsPending = errors.New("jobs: batch results require recovery")

// RecoverResults publishes retained results without invoking a provider runner.
// An incomplete running batch can still add new claims and cannot finalize.
func (s *BatchService) RecoverResults(ctx context.Context, account, id string, files BatchIO) (Batch, error) {
	if files == nil {
		return Batch{}, ErrBatchSubmissionIncomplete
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	batch, err := s.repository.Get(ctx, account, id)
	if err != nil {
		return Batch{}, err
	}
	if batch.ResultsReleased {
		return batch, nil
	}
	if !batch.RunFinished {
		outcome, err := s.buildAggregates(ctx, batch, files)
		if err != nil {
			return batch, err
		}
		batch, err = s.mutate(ctx, account, id, func(current *Batch) error {
			if current.ClaimedLines != batch.ClaimedLines || current.TotalLines != batch.TotalLines {
				return ErrBatchResultsPending
			}
			if current.RunFinished {
				if current.OutputFileID != outcome.outputFileID || current.ErrorFileID != outcome.errorFileID {
					return ErrInvalidBatch
				}
				return nil
			}
			current.OutputFileID, current.ErrorFileID = outcome.outputFileID, outcome.errorFileID
			current.CompletedLines, current.FailedLines = outcome.completed, outcome.failed
			current.RunFinished = true
			if current.State.Terminal() {
				return nil
			}
			return current.Transition(JobStateCompleted, s.now())
		})
		if err != nil {
			return batch, err
		}
	}
	if err := s.validateAggregateReferences(ctx, batch); err != nil {
		return batch, err
	}
	for _, file := range []string{batch.OutputFileID, batch.ErrorFileID} {
		if file != "" {
			if err := files.ConfirmAggregate(ctx, file); err != nil {
				return batch, err
			}
		}
	}
	for number := 1; number <= batch.ClaimedLines; number++ {
		line, err := s.repository.ReadLine(ctx, account, id, number)
		if err != nil {
			return batch, err
		}
		if !line.ResultReady {
			return batch, ErrBatchResultsPending
		}
		if err := files.DeleteResult(ctx, line.OutputFileID); err != nil {
			return batch, err
		}
	}
	return s.mutate(ctx, account, id, func(current *Batch) error {
		if !current.RunFinished || current.OutputFileID != batch.OutputFileID || current.ErrorFileID != batch.ErrorFileID {
			return ErrInvalidBatch
		}
		current.ResultsReleased = true
		return nil
	})
}

// validateAggregateReferences checks all retained line metadata before any
// checkpoint retirement. A retry can run after partial byte cleanup.
func (s *BatchService) validateAggregateReferences(ctx context.Context, batch Batch) error {
	completed, failed := 0, 0
	for number := 1; number <= batch.ClaimedLines; number++ {
		line, err := s.repository.ReadLine(ctx, batch.Account, batch.ID, number)
		if err != nil {
			return err
		}
		if !line.ResultReady {
			return ErrBatchResultsPending
		}
		if line.ResultFailed {
			failed++
		} else {
			completed++
		}
	}
	if completed != batch.CompletedLines || failed != batch.FailedLines ||
		(completed > 0) != (batch.OutputFileID != "") ||
		(failed > 0) != (batch.ErrorFileID != "") ||
		(batch.OutputFileID != "" && batch.OutputFileID == batch.ErrorFileID) {
		return ErrCorruptBatchRecord
	}
	return nil
}

func (s *BatchService) buildAggregates(ctx context.Context, batch Batch, files BatchIO) (runOutcome, error) {
	if batch.State == JobStateQueued ||
		!batch.State.Terminal() && batch.TotalLines == 0 ||
		batch.State != JobStateCancelled && batch.ClaimedLines != batch.TotalLines {
		return runOutcome{}, ErrBatchResultsPending
	}
	for number := 1; number <= batch.ClaimedLines; number++ {
		line, err := s.repository.ReadLine(ctx, batch.Account, batch.ID, number)
		if err != nil {
			return runOutcome{}, err
		}
		if line.ResultDigest == "" {
			return runOutcome{}, ErrBatchResultsPending
		}
		if !line.ResultReady {
			if err := files.RecoverResult(ctx, line.OutputFileID); err != nil {
				return runOutcome{}, err
			}
			if _, err := s.repository.ConfirmLineResult(ctx, line); err != nil {
				return runOutcome{}, err
			}
		}
	}
	outcome := runOutcome{total: batch.TotalLines}
	for _, failed := range []bool{false, true} {
		hash := sha256.New()
		count, size, err := s.copyAggregate(ctx, batch, files, failed, hash)
		if err != nil {
			return runOutcome{}, err
		}
		if count == 0 {
			continue
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		reader, writer := io.Pipe()
		done := make(chan error, 1)
		go func() {
			_, _, err := s.copyAggregate(ctx, batch, files, failed, writer)
			_ = writer.CloseWithError(err)
			done <- err
		}()
		id, storeErr := files.StoreAggregate(ctx, batch, failed, size, digest, reader)
		_ = reader.CloseWithError(storeErr)
		copyErr := <-done
		if storeErr != nil || copyErr != nil {
			return runOutcome{}, errors.Join(storeErr, copyErr)
		}
		if failed {
			outcome.failed = count
			outcome.errorFileID = id
		} else {
			outcome.completed = count
			outcome.outputFileID = id
		}
	}
	return outcome, nil
}

// copyAggregate streams each retained line in input order with constant memory.
func (s *BatchService) copyAggregate(ctx context.Context, batch Batch, files BatchIO, failed bool, destination io.Writer) (int, int64, error) {
	count := 0
	var size int64
	for number := 1; number <= batch.ClaimedLines; number++ {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		line, err := s.repository.ReadLine(ctx, batch.Account, batch.ID, number)
		if err != nil {
			return 0, 0, err
		}
		if !line.ResultReady {
			return 0, 0, ErrBatchResultsPending
		}
		if line.ResultFailed != failed {
			continue
		}
		if line.ResultBytes == math.MaxInt64 || size > math.MaxInt64-line.ResultBytes-1 {
			return 0, 0, ErrInvalidBatch
		}
		reader, err := files.OpenResult(ctx, line.OutputFileID)
		if err != nil {
			return 0, 0, err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(reader, line.ResultBytes+1))
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil {
			return 0, 0, errors.Join(copyErr, closeErr)
		}
		if n != line.ResultBytes || hex.EncodeToString(hash.Sum(nil)) != line.ResultDigest {
			return 0, 0, ErrCorruptBatchRecord
		}
		if _, err := io.WriteString(destination, "\n"); err != nil {
			return 0, 0, err
		}
		count++
		size += n + 1
	}
	return count, size, nil
}
