package jobs_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryReachesBeyondFirstThousandRecords(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		records, err := jobs.OpenRepository(backing)
		require.NoError(t, err)
		for i := range 1001 {
			job := storedJob(t, fmt.Sprintf("job-%04d", i), accountA, submitted)
			require.NoError(t, job.Transition(jobs.JobStateCompleted, submitted.Add(time.Minute)))
			require.NoError(t, records.Create(t.Context(), job))
		}
		service, err := jobs.NewService(records)
		require.NoError(t, err)
		total := 0
		for range 20 {
			result, err := service.Sweep(t.Context())
			require.NoError(t, err)
			total += result.Accounted
			if total == 1001 {
				break
			}
		}
		require.Equal(t, 1001, total, "retained records must not hide later recovery work")
	})
}

func TestRecoverySkipsCorruptionAndReleasesUnreadFinishedBatches(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		records, err := jobs.OpenBatchRepository(backing)
		require.NoError(t, err)
		meter, err := jobslots.Open(backing)
		require.NoError(t, err)
		for _, finished := range []bool{false, true} {
			id := fmt.Sprintf("batch-%t", finished)
			batch, err := jobs.NewBatch(id, accountA, "/v1/chat/completions", "input", submitted)
			require.NoError(t, err)
			batch.SlotID = id
			require.NoError(t, meter.Reserve(t.Context(), accountA, id, id, "batch", 2))
			require.NoError(t, batch.Transition(jobs.JobStateCancelled, submitted.Add(time.Minute)))
			batch.RunFinished = finished
			attachment, err := meter.Attachment(t.Context(), batch.Account, batch.SlotID, batch.ID, "batch")
			require.NoError(t, err)
			require.NoError(t, records.CreateClaimed(t.Context(), batch, attachment))
		}
		require.NoError(t, backing.Set(t.Context(), jobs.BatchStoragePrefix+"corrupt", []byte("broken")))
		service, err := jobs.NewBatchService(records, jobs.WithBatchJobMeter(meter))
		require.NoError(t, err)
		result, err := service.Sweep(t.Context())
		require.ErrorIs(t, err, jobs.ErrCorruptBatchRecord)
		require.Equal(t, 1, result.Failed)
		require.Equal(t, 3, result.Scanned)
		total, err := meter.Total(t.Context(), accountA)
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		for _, finished := range []bool{false, true} {
			batch, err := records.Get(t.Context(), accountA, fmt.Sprintf("batch-%t", finished))
			require.NoError(t, err)
			require.Equal(t, finished, batch.SlotReleased)
		}
	})
}
