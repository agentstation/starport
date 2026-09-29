package recovery

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestHistoryPayloadExecutionUsesProposedOwnerState(t *testing.T) {
	source, reader, _ := kvTransferStores(t, storage.StorageTypeBadger)
	budget, err := reservation.Open(source.(storage.TimeBoundStore))
	require.NoError(t, err)
	at := time.Now().UTC()
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	require.NoError(t, budget.EstablishWindow(t.Context(), meter, at, 0, reservation.History{ID: "history", Proof: "independent-source"}))
	attempt := reservation.Attempt{ID: "attempt", RequestID: "request", AccountID: "owner", KeyID: "key", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: string(routing.OperationVideosGenerations), Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "seconds", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"seconds": 10}}
	_, err = budget.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	before := historyPayloadView(t, reader)
	require.NoError(t, budget.Begin(t.Context(), attempt.ID))
	require.NoError(t, budget.BindJob(t.Context(), attempt.ID, "video"))
	require.NoError(t, budget.MarkUncertain(t.Context(), attempt.ID, "lost-response"))
	repository, err := jobs.OpenRepository(source)
	require.NoError(t, err)
	job, err := jobs.New("video", "owner", "provider", "provider/model", routing.OperationVideosGenerations, at)
	require.NoError(t, err)
	job.KeyID = "key"
	job.CatalogGeneration = "generation"
	job.ReservationID = attempt.ID
	job.Valuation = &attempt.Valuation
	require.NoError(t, job.AdoptProviderJob("private-provider-job"))
	require.NoError(t, repository.Create(t.Context(), job))
	bytes, err := blob.NewFilesystem(filepath.Join(privateKVDirectory(t), "blobs"))
	require.NoError(t, err)
	fileRepository, err := files.OpenRepository(source)
	require.NoError(t, err)
	service, err := files.NewService(fileRepository, bytes)
	require.NoError(t, err)
	uploaded, err := service.Upload(t.Context(), files.UploadRequest{Account: "owner", Filename: "input.jsonl", Purpose: files.PurposeBatch, Size: 3}, strings.NewReader("{}\n"))
	require.NoError(t, err)
	independent := historyPayloadView(t, reader)
	retainedJob, err := jobs.CaptureRecoveryJob(t.Context(), independent, "owner", "video")
	require.NoError(t, err)
	retainedFile, err := files.CaptureRecoveryFile(t.Context(), independent, "owner", uploaded.ID)
	require.NoError(t, err)
	retainedAttempt, err := reservation.ReadBackupAttempt(t.Context(), independent, attempt.ID)
	require.NoError(t, err)
	batch, err := jobs.NewBatch("batch", "owner", "/v1/chat/completions", uploaded.ID, at)
	require.NoError(t, err)
	root := privateKVDirectory(t)
	path := filepath.Join(root, "assets.tar")
	snapshot, err := blob.Backup(t.Context(), bytes, path)
	require.NoError(t, err)
	assets, err := blob.OpenSnapshot(t.Context(), path, root, snapshot)
	require.NoError(t, err)
	defer assets.Close()
	payload := historyKVDomain{Version: 1, Attempts: []reservation.Record{*retainedAttempt}, Files: []files.RecoveryChange{{Account: "owner", ID: uploaded.ID, After: &retainedFile}}, Batches: []jobs.BatchReplay{{Batch: batch, Publish: true}}, Videos: []historyVideoReplay{{Final: retainedJob, Publish: true}}}
	// The old snapshot still says reserved and has no job binding.
	missingAccounting := payload
	missingAccounting.Attempts = nil
	_, err = prepareHistoryKV(t.Context(), "kv_domain", historyPayloadJSON(t, missingAccounting), before, assets, at, nil, revision.RecoveryAuthority{})
	require.Error(t, err)
	prepared, err := prepareHistoryKV(t.Context(), "kv_domain", historyPayloadJSON(t, payload), before, assets, at, nil, revision.RecoveryAuthority{})
	require.NoError(t, err)
	proposal := newHistoryKVProposal(before)
	require.NoError(t, proposal.add(prepared.mutations))
	restored, err := jobs.ReadRecoveryJob(t.Context(), proposal, "owner", "video")
	require.NoError(t, err)
	report, err := jobs.VerifyRecoveryExecution(t.Context(), proposal, restored)
	require.NoError(t, err)
	require.EqualValues(t, 1, report.ReservedJobs)
	for _, change := range prepared.mutations {
		if strings.HasPrefix(change.Key, jobs.BatchStoragePrefix) {
			_, missing, err := jobs.VerifyRecoveryBatch(t.Context(), change.Key, change.NewValue, proposal)
			require.NoError(t, err)
			require.Zero(t, missing)
		}
	}
}
