package app

import (
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/stretchr/testify/require"
)

func TestProductionSweepRecoversUnreadBatchClaim(t *testing.T) {
	fixture := newPerformanceFixture(t, 0)
	application := fixture.application
	require.NotNil(t, application.batches)
	records, err := jobs.OpenBatchRepository(application.store)
	require.NoError(t, err)
	meter, err := jobslots.Open(application.store)
	require.NoError(t, err)
	batch, err := jobs.NewBatch("batch-recovery", "default", "/v1/chat/completions", "input", time.Now())
	require.NoError(t, err)
	batch.SlotID = "claim-recovery"
	require.NoError(t, meter.Reserve(t.Context(), batch.Account, batch.SlotID, batch.ID, "batch", 1))
	require.NoError(t, batch.Transition(jobs.JobStateCompleted, time.Now()))
	batch.RunFinished = true
	require.NoError(t, records.Create(t.Context(), batch))
	application.sweepJobAssets(t.Context())
	recovered, err := records.Get(t.Context(), batch.Account, batch.ID)
	require.NoError(t, err)
	require.True(t, recovered.SlotReleased)
	total, err := meter.Total(t.Context(), batch.Account)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Zero(t, fixture.calls.Load(), "recovery does not dispatch new provider work")
}
