package jobs_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBatchLineClaimIsDurableAndExclusive(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenBatchRepository(store)
		require.NoError(t, err)
		batch, err := jobs.NewBatch("batch", "account", "/v1/chat/completions", "input", time.Now())
		require.NoError(t, err)
		require.NoError(t, records.Create(t.Context(), batch))
		running := batch
		require.NoError(t, running.Transition(jobs.JobStateRunning, time.Now()))
		running.TotalLines = 2
		require.NoError(t, records.Replace(t.Context(), batch, running))
		hash := sha256.Sum256([]byte("{}"))
		digest := hex.EncodeToString(hash[:])
		var group sync.WaitGroup
		results := make(chan error, 8)
		for range 8 {
			group.Go(func() { _, err := records.ClaimLine(t.Context(), batch.Account, batch.ID, 1, digest); results <- err })
		}
		group.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			} else {
				require.ErrorIs(t, err, jobs.ErrBatchLineClaimed)
			}
		}
		require.Equal(t, 1, wins)
		reopened, err := jobs.OpenBatchRepository(store)
		require.NoError(t, err)
		claim, err := reopened.ReadLine(t.Context(), batch.Account, batch.ID, 1)
		require.NoError(t, err)
		require.Equal(t, digest, claim.InputDigest)
		require.NotEmpty(t, claim.RequestID)
		require.Equal(t, 1, claim.Number)
		_, err = reopened.ClaimLine(t.Context(), batch.Account, batch.ID, 1, digest)
		require.ErrorIs(t, err, jobs.ErrBatchLineClaimed)
		_, err = reopened.ReadLine(t.Context(), "other", batch.ID, 1)
		require.ErrorIs(t, err, jobs.ErrBatchLineNotFound)
		current, err := reopened.Get(t.Context(), batch.Account, batch.ID)
		require.NoError(t, err)
		require.Equal(t, 1, current.ClaimedLines)
		changed := current
		changed.ClaimedLines = 0
		require.ErrorIs(t, reopened.Replace(t.Context(), current, changed), jobs.ErrInvalidBatch)
		changed = current
		changed.InputFileID = "different"
		require.ErrorIs(t, reopened.Replace(t.Context(), current, changed), jobs.ErrInvalidBatch)
		canceled := current
		require.NoError(t, canceled.Transition(jobs.JobStateCancelled, time.Now()))
		require.NoError(t, reopened.Replace(t.Context(), current, canceled))
		_, err = reopened.ClaimLine(t.Context(), batch.Account, batch.ID, 2, digest)
		require.ErrorIs(t, err, jobs.ErrBatchAlreadyEnded)
		final, err := reopened.Get(t.Context(), batch.Account, batch.ID)
		require.NoError(t, err)
		require.Equal(t, 1, final.ClaimedLines)
	})
}

type batchClaimFault struct {
	storage.KVStore
	afterWrite bool
	fired      bool
}

func (s *batchClaimFault) CompareAndSwapBatch(ctx context.Context, mutations []storage.CompareAndSwapMutation) error {
	for _, mutation := range mutations {
		if !s.fired && strings.HasPrefix(mutation.Key, "batch-lines:v1:") {
			s.fired = true
			if s.afterWrite {
				if err := s.KVStore.CompareAndSwapBatch(ctx, mutations); err != nil {
					return err
				}
			}
			return errors.New("batch claim storage unavailable")
		}
	}
	return s.KVStore.CompareAndSwapBatch(ctx, mutations)
}

func TestBatchClaimFailureCannotDispatch(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		for _, afterWrite := range []bool{false, true} {
			t.Run(fmt.Sprintf("after-write-%t", afterWrite), func(t *testing.T) {
				fault := &batchClaimFault{KVStore: store, afterWrite: afterWrite}
				records, err := jobs.OpenBatchRepository(fault)
				require.NoError(t, err)
				service, err := jobs.NewBatchService(records, jobs.WithBatchConcurrency(1))
				require.NoError(t, err)
				runner := &echoRunner{}
				batch, err := service.Submit(t.Context(), jobs.BatchSubmission{Account: "account", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: newMemoryBatchIO("{}\n{}\n"), Runner: runner})
				require.NoError(t, err)
				final := waitForTerminalBatch(t, service, batch.Account, batch.ID)
				require.Equal(t, jobs.JobStateFailed, final.State)
				require.Empty(t, runner.ranLines())
				reopened, err := jobs.OpenBatchRepository(store)
				require.NoError(t, err)
				claim, err := reopened.ReadLine(t.Context(), batch.Account, batch.ID, 1)
				if afterWrite {
					require.NoError(t, err)
					require.Equal(t, 1, final.ClaimedLines)
					_, err = reopened.ClaimLine(t.Context(), batch.Account, batch.ID, 1, claim.InputDigest)
					require.ErrorIs(t, err, jobs.ErrBatchLineClaimed)
				} else {
					require.ErrorIs(t, err, jobs.ErrBatchLineNotFound)
					require.Zero(t, final.ClaimedLines)
				}
			})
		}
	})
}
