package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func correctionJob(t *testing.T, store storage.KVStore) (jobs.Repository, jobs.Job) {
	t.Helper()
	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	assets, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	service, err := jobs.NewService(records, jobs.WithAssetStore(assets))
	require.NoError(t, err)
	runner := nativeFixture()
	runner.submitErr = errors.New("lost response")
	job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.Error(t, err)
	_, err = service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", adminInput(t, service, job))
	require.NoError(t, err)
	job, err = records.Get(t.Context(), accountA, job.ID)
	require.NoError(t, err)
	return records, job
}

func correctionIntent(t *testing.T, job jobs.Job, id string) jobs.CorrectionIntent {
	t.Helper()
	request := jobs.ReconciliationRequest{DecisionID: id, Binding: job.CorrectionBinding(""), EvidenceReference: "invoice:123", Reason: "Corrected provider usage", Disposition: "usage", Quantities: reservation.Quantities{"output_seconds": 5}}
	intent, err := job.NewCorrection(request, "key:admin", "", time.Now())
	require.NoError(t, err)
	return intent
}

func TestJobCorrectionHistoryAndOrderedReports(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, job := correctionJob(t, store)
		initial := job.BillingEvidence()
		intent := correctionIntent(t, job, "correction-1")
		pending, err := records.CreateCorrection(t.Context(), job, intent)
		require.NoError(t, err)
		require.Equal(t, initial, pending.BillingEvidence(), "intent grants no capacity and changes no billing")
		err = records.Replace(t.Context(), job, pending)
		require.ErrorIs(t, err, jobs.ErrInvalidJob, "ordinary replacement cannot publish a correction")
		audit, err := records.InspectCorrection(t.Context(), accountA, job.ID, intent.Decision.DecisionID)
		require.NoError(t, err)
		require.False(t, audit.Applied)
		_, err = records.InspectCorrection(t.Context(), accountB, job.ID, intent.Decision.DecisionID)
		require.ErrorIs(t, err, jobs.ErrJobNotFound)
		applied, err := records.ApplyCorrection(t.Context(), pending, nil)
		require.NoError(t, err)
		require.Equal(t, intent.Evidence(), *applied.BillingEvidence())
		// More decisions must remain possible while optional reporting is unavailable.
		current := applied
		for i := 2; i <= 20; i++ {
			next := correctionIntent(t, current, fmt.Sprintf("correction-%d", i))
			current, err = records.CreateCorrection(t.Context(), current, next)
			require.NoError(t, err)
			current, err = records.ApplyCorrection(t.Context(), current, nil)
			require.NoError(t, err)
		}
		reopened, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		current, err = reopened.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		for i := 1; i <= 20; i++ {
			report, err := reopened.NextCorrectionReport(t.Context(), current)
			require.NoError(t, err)
			require.NotNil(t, report)
			require.Equal(t, fmt.Sprintf("correction-%d", i), report.Decision.DecisionID)
			status := "delivered"
			if i == 1 {
				status = "expired"
			}
			current, err = reopened.MarkCorrectionReported(t.Context(), current, *report, status)
			require.NoError(t, err)
			audit, err = reopened.InspectCorrection(t.Context(), accountA, job.ID, report.Decision.DecisionID)
			require.NoError(t, err)
			require.True(t, audit.Applied)
			require.Equal(t, status, audit.ReportStatus)
		}
		report, err := reopened.NextCorrectionReport(t.Context(), current)
		require.NoError(t, err)
		require.Nil(t, report)
		audit, err = reopened.InspectCorrection(t.Context(), accountA, job.ID, intent.Decision.DecisionID)
		require.NoError(t, err)
		require.Equal(t, intent, audit.Intent)
		require.ErrorIs(t, reopened.Delete(t.Context(), accountA, job.ID), jobs.ErrAuditRetention)
	})
}

func TestJobCorrectionSupersedesOnlyPendingIntent(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, job := correctionJob(t, store)
		first := correctionIntent(t, job, "first")
		pending, err := records.CreateCorrection(t.Context(), job, first)
		require.NoError(t, err)
		second := correctionIntent(t, pending, "second")
		replacement, err := records.CreateCorrection(t.Context(), pending, second)
		require.NoError(t, err)
		_, err = records.ApplyCorrection(t.Context(), pending, nil)
		require.ErrorIs(t, err, storage.ErrConflict)
		audit, err := records.InspectCorrection(t.Context(), accountA, job.ID, "first")
		require.NoError(t, err)
		require.False(t, audit.Applied)
		require.Equal(t, "second", audit.SupersededBy)
		applied, err := records.ApplyCorrection(t.Context(), replacement, nil)
		require.NoError(t, err)
		report, err := records.NextCorrectionReport(t.Context(), applied)
		require.NoError(t, err)
		require.Equal(t, "second", report.Decision.DecisionID)
		require.Empty(t, report.PreviousAppliedID)
		changed := second
		changed.Decision.Reason = "different"
		_, err = records.CreateCorrection(t.Context(), applied, changed)
		require.Error(t, err)
	})
}

func TestJobCorrectionConcurrentIntentHasOneWinner(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, job := correctionJob(t, store)
		var group sync.WaitGroup
		outcomes := make(chan error, 8)
		for i := range 8 {
			intent := correctionIntent(t, job, fmt.Sprintf("candidate-%d", i))
			group.Go(func() { _, err := records.CreateCorrection(t.Context(), job, intent); outcomes <- err })
		}
		group.Wait()
		close(outcomes)
		accepted := 0
		for err := range outcomes {
			if err == nil {
				accepted++
			} else {
				require.ErrorIs(t, err, storage.ErrConflict)
			}
		}
		require.Equal(t, 1, accepted)
	})
}

func TestJobCorrectionCommitFailurePreservesIntent(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				records, job := correctionJob(t, store)
				intent := correctionIntent(t, job, "decision")
				pending, err := records.CreateCorrection(t.Context(), job, intent)
				require.NoError(t, err)
				unavailable := errors.New("acknowledgement unavailable")
				_, err = records.ApplyCorrection(t.Context(), pending, func(ctx context.Context, _ jobs.CorrectionIntent, mutations []storage.CompareAndSwapMutation) error {
					if after {
						require.NoError(t, store.CompareAndSwapBatch(ctx, mutations))
					}
					return unavailable
				})
				require.ErrorIs(t, err, unavailable)
				reopened, err := jobs.OpenRepository(store)
				require.NoError(t, err)
				audit, err := reopened.InspectCorrection(t.Context(), accountA, job.ID, "decision")
				require.NoError(t, err)
				require.Equal(t, after, audit.Applied)
				stored, err := reopened.Get(t.Context(), accountA, job.ID)
				require.NoError(t, err)
				if !after {
					stored, err = reopened.ApplyCorrection(t.Context(), stored, nil)
					require.NoError(t, err)
				}
				require.Equal(t, intent.Evidence(), *stored.BillingEvidence())
			})
		})
	}
}
