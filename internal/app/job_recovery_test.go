package app

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/agentstation/starport/internal/jobs/fileio"
	"io"
	"strings"
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
	attachment, err := meter.Attachment(t.Context(), batch.Account, batch.SlotID, batch.ID, "batch")
	require.NoError(t, err)
	require.NoError(t, records.CreateClaimed(t.Context(), batch, attachment))
	application.sweepJobAssets(t.Context())
	recovered, err := records.Get(t.Context(), batch.Account, batch.ID)
	require.NoError(t, err)
	require.True(t, recovered.SlotReleased)
	total, err := meter.Total(t.Context(), batch.Account)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Zero(t, fixture.calls.Load(), "recovery does not dispatch new provider work")
}

func TestProductionSweepPublishesRetainedBatchResults(t *testing.T) {
	fixture := newPerformanceFixture(t, 0)
	application := fixture.application
	r, err := jobs.OpenBatchRepository(application.store)
	require.NoError(t, err)
	batch, err := jobs.NewBatch("aggregate-recovery", "default", "/v1/chat/completions", "input", time.Now())
	require.NoError(t, err)
	batch.TotalLines = 1
	batch.StoredBytesBound = 1024
	require.NoError(t, batch.Transition(jobs.JobStateRunning, time.Now()))
	require.NoError(t, r.Create(t.Context(), batch))
	body := `{"id":"retained"}`
	sum := sha256.Sum256([]byte(body))
	digest := hex.EncodeToString(sum[:])
	line, err := r.ClaimLine(t.Context(), batch.Account, batch.ID, 1, digest)
	require.NoError(t, err)
	adapter := fileio.Store{Files: application.files, Account: batch.Account, StoredBytesBound: batch.StoredBytesBound}
	result, err := adapter.PrepareResult(t.Context(), line)
	require.NoError(t, err)
	line, err = r.BindLineOutput(t.Context(), line, result)
	require.NoError(t, err)
	line, err = r.RecordLineResult(t.Context(), line, digest, int64(len(body)), false)
	require.NoError(t, err)
	require.NoError(t, adapter.StoreResult(t.Context(), result.ID, int64(len(body)), digest, strings.NewReader(body)))
	_, err = r.ConfirmLineResult(t.Context(), line)
	require.NoError(t, err)
	application.sweepJobAssets(t.Context())
	final, err := r.Get(t.Context(), batch.Account, batch.ID)
	require.NoError(t, err)
	require.True(t, final.ResultsReleased)
	require.Equal(t, 1, final.CompletedLines)
	_, reader, err := application.files.Open(t.Context(), batch.Account, final.OutputFileID)
	require.NoError(t, err)
	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, body+"\n", string(content))
	require.Zero(t, fixture.calls.Load(), "the production recovery path never dispatches inference")
}
