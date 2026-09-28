package jobs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type correctionReporter struct {
	err         error
	originalErr error
	corrections []jobs.AccountingCorrection
	originals   []jobs.AccountingEntry
}

func (r *correctionReporter) RecordJob(_ context.Context, entry jobs.AccountingEntry) error {
	r.originals = append(r.originals, entry)
	return r.originalErr
}
func (r *correctionReporter) RecordJobCorrection(_ context.Context, entry jobs.AccountingCorrection) error {
	r.corrections = append(r.corrections, entry)
	return r.err
}

func TestJobCorrectionServiceReportingRecovery(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, job := correctionJob(t, store)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		report := &correctionReporter{err: errors.New("report unavailable")}
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithAccountant(report))
		require.NoError(t, err)
		intent := correctionIntent(t, job, "first")
		view, err := service.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", intent.Decision.ReconciliationRequest)
		require.ErrorIs(t, err, report.err)
		require.Equal(t, "first", view.AppliedCorrectionID)
		require.Empty(t, view.ReportedCorrectionID)
		first, err := records.InspectCorrection(t.Context(), accountA, job.ID, "first")
		require.NoError(t, err)
		require.True(t, first.Applied)
		current, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		second := correctionIntent(t, current, "second")
		view, err = service.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", second.Decision.ReconciliationRequest)
		require.ErrorIs(t, err, report.err)
		require.Equal(t, "second", view.AppliedCorrectionID)
		report.err = nil
		report.corrections = nil
		reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithAccountant(report))
		require.NoError(t, err)
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		require.Len(t, report.corrections, 2)
		require.Equal(t, "first", report.corrections[0].ID)
		require.Equal(t, "second", report.corrections[1].ID)
		require.Equal(t, "first", report.corrections[1].PreviousID)
		require.True(t, report.corrections[0].Original.BillingEvidence.NoCharge, "original usage remains immutable")
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		require.Len(t, report.corrections, 2)
		view, err = reopened.InspectReconciliation(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, "second", view.ReportedCorrectionID)
		_, err = reopened.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", intent.Decision.ReconciliationRequest)
		require.NoError(t, err, "old exact retry cannot undo a successor")
	})
}

func TestJobCorrectionExpiredReportDoesNotUndoSettlement(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, job := correctionJob(t, store)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		report := &correctionReporter{err: jobs.ErrAccountingExpired}
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithAccountant(report))
		require.NoError(t, err)
		intent := correctionIntent(t, job, "expired")
		view, err := service.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", intent.Decision.ReconciliationRequest)
		require.NoError(t, err)
		require.True(t, view.Correction.Applied)
		require.Equal(t, "expired", view.Correction.ReportStatus)
		require.Equal(t, "expired", view.ReportedCorrectionID)
	})
}

func TestJobCorrectionStaleIntentRetainsNewEvidence(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets))
		require.NoError(t, err)
		runner := &heldNativeRunner{nativeRunner: nativeFixture()}
		runner.submitErr = errors.New("lost response")
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		original, err := service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", adminInput(t, service, job))
		require.NoError(t, err)
		job, err = records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		intent := correctionIntent(t, job, "stale")
		_, err = records.CreateCorrection(t.Context(), job, intent)
		require.NoError(t, err)
		answer := runner.acceptance
		answer.NativeResult = &jobs.NativeResult{State: jobs.JobStateCompleted, RequestID: "late-review", Measurement: runner.measurement, Asset: runner.asset}
		// The retained provider response survives an unapplied stale correction.
		_ = runner.recorder.Accepted(t.Context(), answer)
		_, err = service.Sweep(t.Context())
		require.ErrorIs(t, err, jobs.ErrReconciliationConflict)
		view, err := service.InspectReconciliation(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, "correction_review_required", view.Status)
		require.Equal(t, original.Decision, view.Decision)
		require.NotNil(t, view.LateProviderEvidence)
		require.False(t, view.Correction.Applied)
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.True(t, stored.BillingEvidence().NoCharge)
		next := correctionIntent(t, stored, "reviewed-late")
		resolved, err := service.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", next.Decision.ReconciliationRequest)
		require.NoError(t, err)
		require.Equal(t, "reviewed-late", resolved.AppliedCorrectionID)
		old, err := records.InspectCorrection(t.Context(), accountA, job.ID, "stale")
		require.NoError(t, err)
		require.Equal(t, "reviewed-late", old.SupersededBy)
		require.Equal(t, 1, runner.submits)
	})
}

type correctionAuditFault struct {
	storage.KVStore
	after bool
	armed bool
}

func (s *correctionAuditFault) CompareAndSwapBatch(ctx context.Context, mutations []storage.CompareAndSwapMutation) error {
	fire := false
	for _, mutation := range mutations {
		if strings.HasPrefix(mutation.Key, "jobcorrection:") && strings.Contains(mutation.Key, ":intent:") {
			fire = s.armed
		}
	}
	if !fire {
		return s.KVStore.CompareAndSwapBatch(ctx, mutations)
	}
	s.armed = false
	if s.after {
		if err := s.KVStore.CompareAndSwapBatch(ctx, mutations); err != nil {
			return err
		}
	}
	return errors.New("audit acknowledgement lost")
}

func TestJobCorrectionIntentRecoversLostAcknowledgement(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "before"
		if after {
			name = "after"
		}
		t.Run(name, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				_, job := correctionJob(t, store)
				fault := &correctionAuditFault{KVStore: store, after: after, armed: true}
				records, err := jobs.OpenRepository(fault)
				require.NoError(t, err)
				assets, err := blob.NewFilesystem(t.TempDir())
				require.NoError(t, err)
				service, err := jobs.NewService(records, jobs.WithAssetStore(assets))
				require.NoError(t, err)
				intent := correctionIntent(t, job, "retained")
				_, err = service.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", intent.Decision.ReconciliationRequest)
				require.Error(t, err)
				kept, err := records.Get(t.Context(), accountA, job.ID)
				require.NoError(t, err)
				require.True(t, kept.BillingEvidence().NoCharge)
				reopened, err := jobs.OpenRepository(store)
				require.NoError(t, err)
				service, err = jobs.NewService(reopened, jobs.WithAssetStore(assets))
				require.NoError(t, err)
				view, err := service.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", intent.Decision.ReconciliationRequest)
				require.NoError(t, err)
				require.True(t, view.Correction.Applied)
				require.Equal(t, "disabled", view.Correction.ReportStatus)
			})
		})
	}
}

func TestJobCorrectionMissingHistoryRefusesApplication(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, job := correctionJob(t, store)
		pending, err := records.CreateCorrection(t.Context(), job, correctionIntent(t, job, "missing"))
		require.NoError(t, err)
		keys, err := store.ScanWithPrefix(t.Context(), "jobcorrection:", 20)
		require.NoError(t, err)
		for _, key := range keys {
			if strings.Contains(key, ":intent:") {
				require.NoError(t, store.Delete(t.Context(), key))
			}
		}
		_, err = records.ApplyCorrection(t.Context(), pending, nil)
		require.ErrorIs(t, err, jobs.ErrCorruptRecord)
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.True(t, stored.BillingEvidence().NoCharge)
	})
}

func TestJobCorrectionResolvesUnmeasuredLateEvidence(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets))
		require.NoError(t, err)
		runner := &heldNativeRunner{nativeRunner: nativeFixture()}
		runner.submitErr = errors.New("lost response")
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		original, err := service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", adminInput(t, service, job))
		require.NoError(t, err)
		answer := runner.acceptance
		answer.NativeResult = &jobs.NativeResult{State: jobs.JobStateCompleted, RequestID: "unmeasured-response", Asset: runner.asset}
		_ = runner.recorder.Accepted(t.Context(), answer)
		view, err := service.InspectReconciliation(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.NotNil(t, view.LateProviderEvidence)
		require.Nil(t, view.LateProviderEvidence.Measurement)
		require.Equal(t, "provider_evidence_review_required", view.Status)
		request := jobs.ReconciliationRequest{DecisionID: "invoice-no-charge", Binding: view.CorrectionBinding, EvidenceReference: "provider invoice zero", Reason: "Invoice confirms no charge for this request", Disposition: "no_charge"}
		view, err = service.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
		require.NoError(t, err)
		require.Equal(t, "administrator_resolved", view.Status)
		require.Equal(t, original.Decision, view.Decision)
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.False(t, stored.BillingConflict())
		require.True(t, stored.BillingEvidence().NoCharge)
		require.Nil(t, stored.Measurement)
		require.Equal(t, 1, runner.submits)
	})
}

func TestJobCorrectionExpiredOriginalStopsReportingRetries(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		report := &correctionReporter{originalErr: jobs.ErrAccountingExpired, err: jobs.ErrAccountingExpired}
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithAccountant(report))
		require.NoError(t, err)
		runner := nativeFixture()
		runner.submitErr = errors.New("lost response")
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		_, err = service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", adminInput(t, service, job))
		require.NoError(t, err, "expired optional reporting is terminal")
		job, err = records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.False(t, job.Accounted(), "expired reporting must not claim delivery")
		require.False(t, job.ReportingExpiredAt.IsZero())
		cleared := job
		cleared.ReportingExpiredAt = time.Time{}
		require.ErrorIs(t, records.Replace(t.Context(), job, cleared), jobs.ErrInvalidJob)
		intent := correctionIntent(t, job, "after-report-expiry")
		view, err := service.CorrectAdministrator(t.Context(), accountA, job.ID, "key:admin", intent.Decision.ReconciliationRequest)
		require.NoError(t, err)
		require.True(t, view.Correction.Applied)
		require.Equal(t, "expired", view.Correction.ReportStatus)
		require.Equal(t, job.ReportingExpiredAt, view.ReportingExpiredAt)
		require.Equal(t, "administrator_resolved", view.Status)
		reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithAccountant(report))
		require.NoError(t, err)
		for range 2 {
			_, err = reopened.Sweep(t.Context())
			require.NoError(t, err)
		}
		require.Len(t, report.originals, 1)
		require.Len(t, report.corrections, 1)
		require.Equal(t, 1, runner.submits)
	})
}
