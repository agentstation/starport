package jobs_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBatchResumeOnlyProvenUnstartedLines(t *testing.T) {
	repotest.Run(t, func(t *testing.T, kv storage.KVStore) {
		records, err := jobs.OpenBatchRepository(kv)
		require.NoError(t, err)
		batch, err := jobs.NewBatch("resume", "a", "/v1/chat/completions", "input", time.Now())
		require.NoError(t, err)
		require.NoError(t, records.Create(t.Context(), batch))
		next := batch
		require.NoError(t, next.Transition(jobs.JobStateRunning, time.Now()))
		next.TotalLines = 3
		require.NoError(t, records.Replace(t.Context(), batch, next))
		digest := sha256.Sum256([]byte("{}"))
		unknown, err := records.ClaimLine(t.Context(), batch.Account, batch.ID, 1, hex.EncodeToString(digest[:]))
		require.NoError(t, err)
		files := newMemoryBatchIO("{}\n{}\n{}\n")
		runner := &echoRunner{}
		recover := func(context.Context, jobs.Batch) (jobs.LineRunner, error) { return runner, nil }
		open := func(jobs.Batch) jobs.BatchIO { return files }
		first, err := jobs.NewBatchService(records, jobs.WithBatchFiles(open), jobs.WithBatchRecoveryRunner(recover))
		require.NoError(t, err)
		second, err := jobs.NewBatchService(records, jobs.WithBatchFiles(open), jobs.WithBatchRecoveryRunner(recover))
		require.NoError(t, err)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, service := range []*jobs.BatchService{first, second} {
			wg.Go(func() { _, err := service.Sweep(t.Context()); errs <- err })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		require.ElementsMatch(t, []int{2, 3}, runner.ranLines())
		current, err := records.Get(t.Context(), batch.Account, batch.ID)
		require.NoError(t, err)
		require.Equal(t, 3, current.ClaimedLines)
		require.False(t, current.RunFinished, "uncertain work cannot become a completed result")
		kept, err := records.ReadLine(t.Context(), batch.Account, batch.ID, 1)
		require.NoError(t, err)
		require.Equal(t, unknown, kept)
		_, err = first.Sweep(t.Context())
		require.NoError(t, err)
		require.ElementsMatch(t, []int{2, 3}, runner.ranLines())
		require.NoError(t, first.Close(t.Context()))
		require.NoError(t, second.Close(t.Context()))
	})
}

func TestBatchResumeCompletedPrefixAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "continue"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			records, err := jobs.OpenBatchRepository(storage.NewMockStore())
			require.NoError(t, err)
			files := newMemoryBatchIO("{}\n{}\n{}\n")
			worker, err := jobs.NewBatchService(records, jobs.WithBatchConcurrency(1))
			require.NoError(t, err)
			held := &drainingBatchRunner{entered: make(chan struct{}), release: make(chan struct{})}
			batch, err := worker.Submit(t.Context(), jobs.BatchSubmission{Account: "a", Endpoint: "/v1/chat/completions", InputFileID: "input", IO: files, Runner: held})
			require.NoError(t, err)
			<-held.entered
			ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
			require.Error(t, worker.Close(ctx))
			cancel()
			close(held.release)
			require.NoError(t, worker.Close(t.Context()))
			runner := &echoRunner{}
			reopened, err := jobs.NewBatchService(records, jobs.WithBatchFiles(func(jobs.Batch) jobs.BatchIO { return files }), jobs.WithBatchRecoveryRunner(func(context.Context, jobs.Batch) (jobs.LineRunner, error) { return runner, nil }))
			require.NoError(t, err)
			if cancelled {
				_, err = reopened.Cancel(t.Context(), batch.Account, batch.ID)
				require.NoError(t, err)
			}
			_, err = reopened.Sweep(t.Context())
			require.NoError(t, err)
			final, err := reopened.Get(t.Context(), batch.Account, batch.ID)
			require.NoError(t, err)
			require.True(t, final.RunFinished)
			if cancelled {
				require.Empty(t, runner.ranLines())
				require.Equal(t, jobs.JobStateCancelled, final.State)
			} else {
				require.ElementsMatch(t, []int{2, 3}, runner.ranLines())
				require.Equal(t, 3, final.CompletedLines)
			}
			require.NoError(t, reopened.Close(t.Context()))
		})
	}
}
