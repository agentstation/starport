package jobs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBatchResultEvidenceIsImmutable(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := t.Context()
		repo, err := jobs.OpenBatchRepository(store)
		require.NoError(t, err)
		batch, err := jobs.NewBatch("batch", "a", "/v1/chat/completions", "input", time.Now())
		require.NoError(t, err)
		require.NoError(t, repo.Create(ctx, batch))
		running := batch
		require.NoError(t, running.Transition(jobs.JobStateRunning, time.Now()))
		running.TotalLines = 1
		require.NoError(t, repo.Replace(ctx, batch, running))
		sum := sha256.Sum256([]byte("input"))
		digest := hex.EncodeToString(sum[:])
		claim, err := repo.ClaimLine(ctx, "a", "batch", 1, digest)
		require.NoError(t, err)
		file := jobs.ResultFile{ID: "output", ExpiresAt: time.Now().Add(time.Hour)}
		bound, err := repo.BindLineOutput(ctx, claim, file)
		require.NoError(t, err)
		_, err = repo.BindLineOutput(ctx, claim, jobs.ResultFile{ID: "changed", ExpiresAt: file.ExpiresAt})
		require.ErrorIs(t, err, jobs.ErrInvalidBatch)
		retained, err := repo.RecordLineResult(ctx, bound, digest, 5, false)
		require.NoError(t, err)
		require.False(t, retained.ResultReady)
		_, err = repo.RecordLineResult(ctx, bound, digest, 6, false)
		require.ErrorIs(t, err, jobs.ErrInvalidBatch)
		_, err = repo.RecordLineResult(ctx, bound, digest, 5, true)
		require.ErrorIs(t, err, jobs.ErrInvalidBatch)
		_, err = repo.ConfirmLineResult(ctx, bound)
		require.ErrorIs(t, err, jobs.ErrInvalidBatch)
		ready, err := repo.ConfirmLineResult(ctx, retained)
		require.NoError(t, err)
		require.True(t, ready.ResultReady)
		_, err = repo.ConfirmLineResult(ctx, retained)
		require.NoError(t, err)
		reopened, err := jobs.OpenBatchRepository(store)
		require.NoError(t, err)
		actual, err := reopened.ReadLine(ctx, "a", "batch", 1)
		require.NoError(t, err)
		require.Equal(t, ready, actual)
		forged := retained
		forged.RequestID = "another-request"
		_, err = repo.ConfirmLineResult(ctx, forged)
		require.ErrorIs(t, err, jobs.ErrInvalidBatch)
	})
}

type interruptedResultIO struct {
	*memoryBatchIO
	after bool
}

func (b interruptedResultIO) StoreResult(ctx context.Context, id string, size int64, digest string, body io.Reader) error {
	if b.after {
		if err := b.memoryBatchIO.StoreResult(ctx, id, size, digest, body); err != nil {
			return err
		}
	}
	return errors.New("result write acknowledgement unavailable")
}

func TestResultStorageFailureStopsDispatchAndRetainsCapacity(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		for _, after := range []bool{false, true} {
			name := "before"
			if after {
				name = "after"
			}
			t.Run(name, func(t *testing.T) {
				repo, err := jobs.OpenBatchRepository(store)
				require.NoError(t, err)
				meter, err := jobslots.Open(store)
				require.NoError(t, err)
				service, err := jobs.NewBatchService(repo, jobs.WithBatchJobMeter(meter), jobs.WithBatchConcurrency(1))
				require.NoError(t, err)
				runner := &echoRunner{}
				fileIO := interruptedResultIO{newMemoryBatchIO("{}\n{}\n{}\n"), after}
				batch, err := service.Submit(t.Context(), jobs.BatchSubmission{Account: name, Endpoint: "/v1/chat/completions", InputFileID: "input", OutstandingBound: 1, IO: fileIO, Runner: runner})
				require.NoError(t, err)
				final := waitForTerminalBatch(t, service, name, batch.ID)
				require.Equal(t, jobs.JobStateFailed, final.State)
				require.Equal(t, []int{1}, runner.ranLines())
				require.False(t, final.RunFinished)
				require.False(t, final.SlotReleased)
				require.Zero(t, final.CompletedLines)
				claim, err := repo.ReadLine(t.Context(), name, batch.ID, 1)
				require.NoError(t, err)
				require.NotEmpty(t, claim.OutputFileID)
				require.NotEmpty(t, claim.ResultDigest)
				require.False(t, claim.ResultReady)
				total, err := meter.Total(t.Context(), name)
				require.NoError(t, err)
				require.Equal(t, int64(1), total)
				if after {
					reader, err := fileIO.OpenResult(t.Context(), claim.OutputFileID)
					require.NoError(t, err)
					require.NoError(t, reader.Close())
				}
			})
		}
	})
}

type resultConfirmationLostAck struct{ storage.KVStore }

func (s resultConfirmationLostAck) CompareAndSwap(ctx context.Context, key string, previous, next []byte) error {
	err := s.KVStore.CompareAndSwap(ctx, key, previous, next)
	if err == nil && strings.HasPrefix(key, "batch-lines:v1:") && bytes.Contains(next, []byte(`"result_ready":true`)) {
		return context.DeadlineExceeded
	}
	return err
}
func TestResultConfirmationLostAckDoesNotDispatchAgain(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repo, err := jobs.OpenBatchRepository(resultConfirmationLostAck{store})
		require.NoError(t, err)
		meter, err := jobslots.Open(store)
		require.NoError(t, err)
		service, err := jobs.NewBatchService(repo, jobs.WithBatchConcurrency(1), jobs.WithBatchJobMeter(meter))
		require.NoError(t, err)
		runner := &echoRunner{}
		fileIO := newMemoryBatchIO("{}\n{}\n")
		batch, err := service.Submit(t.Context(), jobs.BatchSubmission{Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", OutstandingBound: 1, IO: fileIO, Runner: runner})
		require.NoError(t, err)
		final := waitForTerminalBatch(t, service, "a", batch.ID)
		require.False(t, final.RunFinished)
		require.Equal(t, []int{1}, runner.ranLines())
		claim, err := repo.ReadLine(t.Context(), "a", batch.ID, 1)
		require.NoError(t, err)
		require.True(t, claim.ResultReady)
		reader, err := fileIO.OpenResult(t.Context(), claim.OutputFileID)
		require.NoError(t, err)
		result, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.NotEmpty(t, result)
		total, err := meter.Total(t.Context(), "a")
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
	})
}
