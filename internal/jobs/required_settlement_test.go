package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRequiredReservationCannotUseOptionalAccounting(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		job, err := jobs.New("required-job", accountA, "fixture", "fixture/model", routing.OperationVideosGenerations, time.Now())
		require.NoError(t, err)
		job.KeyID, job.CatalogGeneration, job.ReservationID = "key", "generation", "reservation"
		require.NoError(t, job.AdoptProviderJob("private-provider-job"))
		require.NoError(t, job.Transition(jobs.JobStateCompleted, time.Now()))
		require.NoError(t, records.Create(t.Context(), job))
		optional := &recordingAccountant{}
		service, err := jobs.NewService(records, jobs.WithAccountant(optional))
		require.NoError(t, err)
		result, err := service.Sweep(t.Context())
		require.Error(t, err, "a required reservation has no settlement owner")
		require.Equal(t, 1, result.Failed)
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.False(t, stored.Accounted(), "a terminal state is not charge evidence")
		require.Empty(t, optional.all())
	})
}

type requiredJobRunner struct{ *recordingRunner }

func (r requiredJobRunner) Submit(ctx context.Context, recorder jobs.SubmissionRecorder) (jobs.Acceptance, error) {
	if err := recorder.BeforeDispatch(ctx, jobs.Dispatch{Provider: r.acceptance.Provider, Model: r.acceptance.Model, CatalogGeneration: "generation", ReservationID: "reservation"}); err != nil {
		return jobs.Acceptance{}, err
	}
	r.submits++
	return r.acceptance, recorder.Accepted(ctx, r.acceptance)
}

type requiredJobOwner struct {
	bound      jobs.Job
	bindErr    error
	confirmErr error
}

func (o *requiredJobOwner) BindJob(_ context.Context, job jobs.Job) error {
	o.bound = job
	return o.bindErr
}

func (o *requiredJobOwner) ConfirmJob(_ context.Context, job jobs.Job) error {
	if o.bound.ID != job.ID || o.bound.ReservationID != job.ReservationID {
		return errors.New("job binding differs")
	}
	return o.confirmErr
}

func TestRequiredJobBindingPrecedesDispatch(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "missing owner"
		if configured {
			name = "binding unavailable"
		}
		t.Run(name, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				records, err := jobs.OpenRepository(store)
				require.NoError(t, err)
				var options []jobs.ServiceOption
				if configured {
					options = append(options, jobs.WithRequiredSettlement(&requiredJobOwner{bindErr: errors.New("storage unavailable")}))
				}
				service, err := jobs.NewService(records, options...)
				require.NoError(t, err)
				runner := requiredJobRunner{acceptedRunner()}
				_, err = service.Submit(t.Context(), open(runner), submissionFor(accountA))
				require.Error(t, err)
				require.Zero(t, runner.submits)
				page, err := records.RecoveryPage(t.Context(), "")
				require.NoError(t, err)
				require.Empty(t, page.Records)
			})
		})
	}
}

func TestRequiredSettlementDoesNotRetainTerminalSlot(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		meter, err := jobslots.Open(store)
		require.NoError(t, err)
		owner := &requiredJobOwner{confirmErr: errors.New("missing provider evidence")}
		optional := &recordingAccountant{err: errors.New("optional report unavailable")}
		service, err := jobs.NewService(records, jobs.WithJobMeter(meter), jobs.WithRequiredSettlement(owner), jobs.WithAccountant(optional))
		require.NoError(t, err)
		runner := requiredJobRunner{acceptedRunner()}
		runner.poll = jobs.Report{State: jobs.JobStateCompleted}
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.NoError(t, err)
		ended, err := service.Refresh(t.Context(), runner, accountA, job.ID)
		require.NoError(t, err)
		require.True(t, ended.SlotReleased)
		require.False(t, ended.Accounted())
		require.Empty(t, optional.all())
		total, err := meter.Total(t.Context(), accountA)
		require.NoError(t, err)
		require.Zero(t, total)
		owner.confirmErr = nil
		reopened, err := jobs.NewService(records, jobs.WithRequiredSettlement(owner), jobs.WithAccountant(optional))
		require.NoError(t, err)
		result, err := reopened.Sweep(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, result.Accounted)
		require.Len(t, optional.all(), 1)
		// A previous optional mark must not bypass required evidence after recovery.
		owner.confirmErr = errors.New("authority no longer approved")
		_, err = reopened.Sweep(t.Context())
		require.ErrorIs(t, err, jobs.ErrSettlementPending)
		require.Len(t, optional.all(), 1)
	})
}
