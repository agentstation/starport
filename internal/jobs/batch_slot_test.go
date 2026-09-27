package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type observedJobRelease struct {
	*limits.JobMeter
	released chan error
}

func (m *observedJobRelease) Release(ctx context.Context, holder string, count int64) error {
	err := m.JobMeter.Release(ctx, holder, count)
	m.released <- err
	return err
}

func TestUnboundedBatchPreservesOutstandingVideoSlot(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		meter, err := limits.NewJobMeter(store)
		require.NoError(t, err)
		videoRecords, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		videos, err := jobs.NewService(videoRecords, jobs.WithJobMeter(meter))
		require.NoError(t, err)
		_, err = videos.Submit(t.Context(), open(acceptedRunner()), submissionFor(accountA))
		require.NoError(t, err)

		batchRecords, err := jobs.OpenBatchRepository(store)
		require.NoError(t, err)
		observed := &observedJobRelease{JobMeter: meter, released: make(chan error, 1)}
		batches, err := jobs.NewBatchService(batchRecords, jobs.WithBatchJobMeter(observed))
		require.NoError(t, err)
		runner := newBlockingRunner()
		batch, err := batches.Submit(t.Context(), jobs.BatchSubmission{
			Account: accountA, Endpoint: "/v1/chat/completions", InputFileID: "input",
			IO: newMemoryBatchIO("{}\n"), Runner: runner, OutstandingBound: 0,
		})
		require.NoError(t, err)
		defer func() {
			close(runner.release)
			select {
			case err := <-observed.released:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("batch did not release its slot")
			}
			total, err := meter.Total(t.Context(), accountA)
			require.NoError(t, err)
			require.Equal(t, int64(1), total, "batch completion must preserve the active video slot")
			require.ErrorIs(t, meter.Reserve(t.Context(), accountA, 1, 1), limits.ErrTooManyOutstandingJobs)
			final, err := batches.Get(t.Context(), accountA, batch.ID)
			require.NoError(t, err)
			require.Equal(t, jobs.JobStateCompleted, final.State)
		}()
		select {
		case <-runner.started:
		case <-time.After(5 * time.Second):
			t.Fatal("batch did not dispatch its line")
		}
		total, err := meter.Total(t.Context(), accountA)
		require.NoError(t, err)
		require.Equal(t, int64(2), total, "unbounded work still owns its outstanding slot")
	})
}
