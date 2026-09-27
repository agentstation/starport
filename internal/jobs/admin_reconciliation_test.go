package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func adminInput(t *testing.T, service *jobs.Service, job jobs.Job) jobs.ReconciliationRequest {
	t.Helper()
	view, err := service.InspectReconciliation(t.Context(), job.Account, job.ID)
	require.NoError(t, err)
	return jobs.ReconciliationRequest{DecisionID: "decision-1", Binding: view.Binding, EvidenceReference: "provider-usage:case-17", Reason: "Provider confirmed no charge", Disposition: "no_charge"}
}

func TestAdministratorReconciliationRestartAndIsolation(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		meter, err := jobslots.Open(store)
		require.NoError(t, err)
		optional := &recordingAccountant{}
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithJobMeter(meter), jobs.WithAccountant(optional), jobs.WithClock(func() time.Time { return submitted }))
		require.NoError(t, err)
		runner := nativeFixture()
		runner.submitErr = errors.New("response lost")
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		require.True(t, job.SubmissionPending)
		request := adminInput(t, service, job)
		_, err = service.ReconcileAdministrator(t.Context(), accountB, job.ID, "key:admin-id", request)
		require.ErrorIs(t, err, jobs.ErrJobNotFound)
		_, err = service.ReconcileAdministrator(t.Context(), accountA, job.ID, "anonymous", request)
		require.ErrorIs(t, err, jobs.ErrReconciliationInvalid)
		for _, mutate := range []func(*jobs.ReconciliationRequest){
			func(r *jobs.ReconciliationRequest) { r.Binding = "stale" },
			func(r *jobs.ReconciliationRequest) { r.EvidenceReference = "" },
			func(r *jobs.ReconciliationRequest) { r.Reason = "" },
			func(r *jobs.ReconciliationRequest) { r.Disposition = "timeout" },
			func(r *jobs.ReconciliationRequest) { r.Quantities = reservation.Quantities{"output_seconds": 0} },
			func(r *jobs.ReconciliationRequest) { r.Disposition = "usage" },
		} {
			invalid := request
			mutate(&invalid)
			_, err = service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin-id", invalid)
			require.ErrorIs(t, err, jobs.ErrReconciliationInvalid)
		}
		total, err := meter.Total(t.Context(), accountA)
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		view, err := service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin-id", request)
		require.NoError(t, err)
		require.Equal(t, "administrator_resolved", view.Status)
		require.Equal(t, request, view.Decision.ReconciliationRequest)
		require.Equal(t, "key:admin-id", view.Decision.Actor)
		require.Equal(t, submitted, view.Decision.DecidedAt)
		require.True(t, view.Accounted)
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Nil(t, stored.Measurement)
		require.True(t, stored.BillingEvidence().NoCharge)
		require.True(t, stored.SlotReleased)
		require.Len(t, optional.all(), 1)
		require.Nil(t, optional.all()[0].Measurement)
		require.True(t, optional.all()[0].BillingEvidence.NoCharge)
		require.ErrorIs(t, records.Delete(t.Context(), accountA, job.ID), jobs.ErrAuditRetention)
		require.ErrorIs(t, records.Replace(t.Context(), stored, job), jobs.ErrInvalidJob)
		reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithJobMeter(meter), jobs.WithAccountant(optional), jobs.WithClock(func() time.Time { return submitted.Add(time.Hour) }))
		require.NoError(t, err)
		replay, err := reopened.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin-id", request)
		require.NoError(t, err)
		require.Equal(t, view, replay)
		request.Reason = "different claim"
		_, err = reopened.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin-id", request)
		require.ErrorIs(t, err, jobs.ErrReconciliationConflict)
		require.Len(t, optional.all(), 1)
		total, err = meter.Total(t.Context(), accountA)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Equal(t, 1, runner.submits)
		require.Zero(t, runner.polls)
	})
}

type interruptedAdminDecision struct {
	jobs.Repository
	after bool
}

func (r interruptedAdminDecision) Replace(ctx context.Context, old, next jobs.Job) error {
	if old.SubmissionPending && next.ReconciliationStatus() == "administrator_recorded" {
		if r.after {
			if err := r.Repository.Replace(ctx, old, next); err != nil {
				return err
			}
		}
		return errors.New("audit write unavailable")
	}
	return r.Repository.Replace(ctx, old, next)
}

func TestAdministratorAuditPrecedesRelease(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "before commit"
		if after {
			name = "lost acknowledgement"
		}
		t.Run(name, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				records, err := jobs.OpenRepository(store)
				require.NoError(t, err)
				assets, err := blob.NewFilesystem(t.TempDir())
				require.NoError(t, err)
				meter, err := jobslots.Open(store)
				require.NoError(t, err)
				service, err := jobs.NewService(interruptedAdminDecision{records, after}, jobs.WithAssetStore(assets), jobs.WithJobMeter(meter))
				require.NoError(t, err)
				runner := nativeFixture()
				runner.submitErr = errors.New("lost")
				job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
				require.Error(t, err)
				request := adminInput(t, service, job)
				_, err = service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
				require.Error(t, err)
				total, err := meter.Total(t.Context(), accountA)
				require.NoError(t, err)
				require.EqualValues(t, 1, total)
				view, err := service.InspectReconciliation(t.Context(), accountA, job.ID)
				require.NoError(t, err)
				require.Equal(t, after, view.Decision != nil)
				require.False(t, view.Accounted)
				reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithJobMeter(meter))
				require.NoError(t, err)
				if after {
					result, err := reopened.Sweep(t.Context())
					require.NoError(t, err)
					require.Equal(t, 1, result.Accounted)
				} else {
					result, err := reopened.Sweep(t.Context())
					require.NoError(t, err)
					require.Equal(t, 1, result.AwaitingReconciliation)
				}
				_, err = reopened.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
				require.NoError(t, err)
				total, err = meter.Total(t.Context(), accountA)
				require.NoError(t, err)
				require.Zero(t, total)
			})
		})
	}
}

func TestConcurrentAdministratorDecisions(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		first, err := jobs.NewService(records, jobs.WithAssetStore(assets))
		require.NoError(t, err)
		second, err := jobs.NewService(records, jobs.WithAssetStore(assets))
		require.NoError(t, err)
		runner := nativeFixture()
		runner.submitErr = errors.New("lost")
		job, err := first.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		request := adminInput(t, first, job)
		usage := request
		usage.DecisionID, usage.Disposition, usage.Quantities = "usage-decision", "usage", reservation.Quantities{"output_seconds": 5}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, entry := range []struct {
			service *jobs.Service
			request jobs.ReconciliationRequest
		}{{first, request}, {second, usage}} {
			wg.Go(func() {
				<-start
				_, _ = entry.service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", entry.request)
			})
		}
		close(start)
		wg.Wait()
		view, err := first.InspectReconciliation(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.NotNil(t, view.Decision)
		accepted := view.Decision.ReconciliationRequest
		_, err = second.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", accepted)
		require.NoError(t, err)
		loser := request
		if accepted.DecisionID == request.DecisionID {
			loser = usage
		}
		_, err = first.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", loser)
		require.ErrorIs(t, err, jobs.ErrReconciliationConflict)
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Nil(t, stored.Measurement)
		require.Equal(t, accepted.Quantities, stored.BillingEvidence().Quantities)
	})
}

// heldNativeRunner simulates a response that arrives after the request was lost.
type heldNativeRunner struct {
	*nativeRunner
	recorder jobs.SubmissionRecorder
}

func (r *heldNativeRunner) Submit(ctx context.Context, recorder jobs.SubmissionRecorder) (jobs.Acceptance, error) {
	r.recorder = recorder
	return r.nativeRunner.Submit(ctx, recorder)
}

func TestLateProviderEvidenceCannotReplaceAdministratorDecision(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets))
		require.NoError(t, err)
		runner := &heldNativeRunner{nativeRunner: nativeFixture()}
		runner.submitErr = errors.New("lost")
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		request := adminInput(t, service, job)
		original, err := service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
		require.NoError(t, err)
		answer := runner.acceptance
		answer.NativeResult = &jobs.NativeResult{State: jobs.JobStateCompleted, RequestID: "private-late-provider", Measurement: runner.measurement, Asset: runner.asset}
		require.NoError(t, runner.recorder.Accepted(t.Context(), answer))
		view, err := service.InspectReconciliation(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, original.Decision, view.Decision)
		require.Equal(t, "provider_evidence_review_required", view.Status)
		require.Equal(t, "private-late-provider", view.LateProviderEvidence.RequestID)
		require.Equal(t, runner.measurement.Quantities, view.LateProviderEvidence.Measurement.Quantities)
		_, err = service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
		require.ErrorIs(t, err, jobs.ErrReconciliationConflict)
		reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets))
		require.NoError(t, err)
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		recovered, err := reopened.InspectReconciliation(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, view, recovered)
		require.Equal(t, 1, runner.submits)
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.True(t, stored.BillingEvidence().NoCharge)
		require.Nil(t, stored.Measurement)
		require.False(t, stored.HasAsset())
	})
}

func TestAdministratorDecisionSurvivesSettlementFailure(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		unavailable := errors.New("budget authority unavailable")
		owner := &requiredJobOwner{confirmErr: unavailable}
		optional := &recordingAccountant{}
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithRequiredSettlement(owner), jobs.WithAccountant(optional))
		require.NoError(t, err)
		runner := nativeFixture()
		runner.reservationID, runner.submitErr = "original-reservation", errors.New("lost response")
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		request := adminInput(t, service, job)
		view, err := service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
		require.ErrorIs(t, err, unavailable)
		require.Equal(t, "administrator_recorded", view.Status)
		require.NotNil(t, view.Decision)
		require.Empty(t, optional.all())
		reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithRequiredSettlement(owner), jobs.WithAccountant(optional))
		require.NoError(t, err)
		owner.confirmErr = nil
		recovered, err := reopened.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
		require.NoError(t, err)
		require.Equal(t, view.Decision, recovered.Decision)
		require.Equal(t, "administrator_resolved", recovered.Status)
		require.Len(t, optional.all(), 1)
		require.Equal(t, 1, runner.submits)
	})
}

type interruptedLateEvidence struct{ jobs.Repository }

func (r interruptedLateEvidence) Replace(ctx context.Context, old, next jobs.Job) error {
	if next.ReconciliationStatus() == "provider_evidence_review_required" {
		return errors.New("late evidence write unavailable")
	}
	return r.Repository.Replace(ctx, old, next)
}

func TestLateEvidenceSurvivesAuditWriteFailureAndAssetExpiry(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		blobs := &interruptedNativeAssets{Store: assets}
		now := submitted
		service, err := jobs.NewService(interruptedLateEvidence{records}, jobs.WithAssetStore(blobs), jobs.WithRetention(time.Hour), jobs.WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		runner := &heldNativeRunner{nativeRunner: nativeFixture()}
		runner.submitErr = errors.New("lost")
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		input := adminInput(t, service, job)
		original, err := service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", input)
		require.NoError(t, err)
		answer := runner.acceptance
		answer.NativeResult = &jobs.NativeResult{State: jobs.JobStateCompleted, RequestID: "late", Measurement: runner.measurement, Asset: runner.asset}
		require.Error(t, runner.recorder.Accepted(t.Context(), answer))
		receipt, err := blobs.Get(t.Context(), blobs.firstKey)
		require.NoError(t, err, "an audit write failure must retain the original receipt")
		require.NoError(t, receipt.Close())
		reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		saved, err := reopened.InspectReconciliation(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, original.Decision, saved.Decision)
		require.NotNil(t, saved.LateProviderEvidence)
		receipt, err = assets.Get(t.Context(), blobs.firstKey)
		require.NoError(t, err, "private response keeps its original retention deadline")
		require.NoError(t, receipt.Close())
		now = now.Add(time.Hour)
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		_, err = assets.Get(t.Context(), blobs.firstKey)
		require.ErrorIs(t, err, blob.ErrNotFound)
		after, err := reopened.InspectReconciliation(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, saved, after, "asset expiry cannot erase billing evidence")
	})
}

func TestMatchingLateEvidencePreservesResolvedDecision(t *testing.T) {
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
		request := adminInput(t, service, job)
		request.Disposition, request.Quantities = "usage", runner.measurement.Quantities
		accepted, err := service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
		require.NoError(t, err)
		answer := runner.acceptance
		answer.NativeResult = &jobs.NativeResult{State: jobs.JobStateCompleted, RequestID: "matching-late-response", Measurement: runner.measurement, Asset: runner.asset}
		require.NoError(t, runner.recorder.Accepted(t.Context(), answer))
		view, err := service.ReconcileAdministrator(t.Context(), accountA, job.ID, "key:admin", request)
		require.NoError(t, err)
		require.Equal(t, accepted.Decision, view.Decision)
		require.NotNil(t, view.LateProviderEvidence)
		require.Equal(t, "administrator_resolved", view.Status)
		require.True(t, view.Accounted)
	})
}
