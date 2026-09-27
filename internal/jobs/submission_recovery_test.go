package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type refusedSubmissionStore struct{ jobs.Repository }

func (r refusedSubmissionStore) Create(context.Context, jobs.Job) error {
	return errors.New("job storage unavailable")
}

func TestSubmissionPersistenceFailurePreventsProviderCall(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		service, err := jobs.NewService(refusedSubmissionStore{records})
		require.NoError(t, err)
		runner := acceptedRunner()
		_, err = service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		require.Zero(t, runner.submits, "persistence failure must precede provider dispatch")
	})
}

func TestAmbiguousSubmissionRetainsRecordAndSlot(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		meter := &countingMeter{}
		service, err := jobs.NewService(records, jobs.WithJobMeter(meter), jobs.WithIdentifiers(func() string { return "job_uncertain" }))
		require.NoError(t, err)
		runner := acceptedRunner()
		runner.submitErr = errors.New("provider response lost")
		_, err = service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.Error(t, err)
		held, err := records.Get(t.Context(), accountA, "job_uncertain")
		require.NoError(t, err, "a potentially accepted submission must remain recoverable")
		require.False(t, held.HasProviderJob())
		require.True(t, held.SubmissionPending)
		reopened, err := jobs.NewService(records, jobs.WithJobMeter(meter), jobs.WithClock(func() time.Time { return held.CreatedAt.Add(2 * jobs.DefaultLifetime) }))
		require.NoError(t, err)
		result, err := reopened.Sweep(t.Context())
		require.NoError(t, err)
		require.Zero(t, result.AwaitingReconciliation)
		require.Zero(t, result.Accounted)
		_, err = reopened.Refresh(t.Context(), runner, accountA, held.ID)
		require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
		_, err = reopened.Cancel(t.Context(), runner, accountA, held.ID)
		require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
		require.Zero(t, runner.polls)
		require.Zero(t, runner.cancels)
		require.Equal(t, 1, runner.submits)
		reserves, releases := meter.counts()
		require.Equal(t, 1, reserves)
		require.Zero(t, releases)
	})
}

type interruptedAcceptance struct {
	jobs.Repository
	after bool
}

func (r interruptedAcceptance) Replace(ctx context.Context, expected, next jobs.Job) error {
	if expected.SubmissionPending && !next.SubmissionPending {
		if r.after {
			if err := r.Repository.Replace(ctx, expected, next); err != nil {
				return err
			}
		}
		return errors.New("acceptance write acknowledgement unavailable")
	}
	return r.Repository.Replace(ctx, expected, next)
}

func TestSubmissionAcceptanceWriteRecovery(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "before commit"
		if after {
			name = "after commit"
		}
		t.Run(name, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				records, err := jobs.OpenRepository(store)
				require.NoError(t, err)
				service, err := jobs.NewService(interruptedAcceptance{records, after}, jobs.WithIdentifiers(func() string { return "job_acceptance" }))
				require.NoError(t, err)
				runner := acceptedRunner()
				_, err = service.Submit(t.Context(), open(runner), submissionFor(accountA))
				if after {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
				}
				stored, err := records.Get(t.Context(), accountA, "job_acceptance")
				require.NoError(t, err)
				require.Equal(t, !after, stored.SubmissionPending)
				require.Equal(t, after, stored.HasProviderJob())
				require.Equal(t, 1, runner.submits)
			})
		})
	}
}

func TestSubmissionAcceptanceSurvivesCallerCancellation(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		service, err := jobs.NewService(records)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		runner := acceptedRunner()
		runner.onSubmit = cancel
		job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
		require.NoError(t, err)
		stored, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.False(t, stored.SubmissionPending)
		require.True(t, stored.HasProviderJob())
	})
}
