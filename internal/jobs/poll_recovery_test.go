package jobs_test

import (
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestExplicitReconciliationPreservesAndResolvesCapacity(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		ctx := t.Context()
		now := submitted
		records, err := jobs.OpenRepository(backing)
		require.NoError(t, err)
		meter, err := jobslots.Open(backing)
		require.NoError(t, err)
		service, err := jobs.NewService(records, jobs.WithJobMeter(meter), jobs.WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		runner := acceptedRunner()
		job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
		require.NoError(t, err)
		now = submitted.Add(2 * jobs.DefaultLifetime)
		require.True(t, service.NeedsReconciliation(job))
		_, err = service.Reconcile(ctx, runner, accountB, job.ID)
		require.ErrorIs(t, err, jobs.ErrJobNotFound)
		require.Zero(t, runner.polls)
		runner.pollErr = errors.New("provider unavailable")
		_, err = service.Reconcile(ctx, runner, accountA, job.ID)
		require.ErrorIs(t, err, runner.pollErr)
		runner.pollErr = nil
		runner.poll = jobs.Report{State: jobs.JobStateRunning}
		held, err := service.Reconcile(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
		require.True(t, service.NeedsReconciliation(held))
		require.False(t, held.SlotReleased)
		total, err := meter.Total(ctx, accountA)
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		_, err = service.Refresh(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, 2, runner.polls, "explicit reconciliation cannot restart automatic polling")
		reopened, err := jobs.NewService(records, jobs.WithJobMeter(meter), jobs.WithClock(func() time.Time { return now }))
		require.NoError(t, err)
		runner.poll = jobs.Report{State: jobs.JobStateFailed, Reason: "provider confirmed failure"}
		resolved, err := reopened.Reconcile(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, jobs.JobStateFailed, resolved.State)
		require.Equal(t, runner.poll.Reason, resolved.Reason)
		require.True(t, resolved.SlotReleased)
		require.False(t, reopened.NeedsReconciliation(resolved))
		_, err = reopened.Reconcile(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, 3, runner.polls)
		require.Equal(t, 1, runner.submits)
		total, err = meter.Total(ctx, accountA)
		require.NoError(t, err)
		require.Zero(t, total)
	})
}

func TestPollingExhaustionRetainsProviderWork(t *testing.T) {
	for _, action := range []string{"read", "sweep"} {
		t.Run(action, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
				ctx := t.Context()
				now := submitted
				records, err := jobs.OpenRepository(backing)
				require.NoError(t, err)
				meter, err := jobslots.Open(backing)
				require.NoError(t, err)
				accountant := &recordingAccountant{}
				service, err := jobs.NewService(records, jobs.WithJobMeter(meter), jobs.WithAccountant(accountant), jobs.WithClock(func() time.Time { return now }))
				require.NoError(t, err)
				runner := acceptedRunner()
				submission := submissionFor(accountA)
				submission.OutstandingBound = 1
				job, err := service.Submit(ctx, open(runner), submission)
				require.NoError(t, err)
				now = submitted.Add(jobs.DefaultLifetime)
				if action == "read" {
					_, err = service.Refresh(ctx, runner, accountA, job.ID)
				} else {
					_, err = service.Sweep(ctx)
				}
				require.NoError(t, err)
				stored, err := records.Get(ctx, accountA, job.ID)
				require.NoError(t, err)
				require.Equal(t, jobs.JobStateQueued, stored.State)
				require.True(t, stored.TerminalAt.IsZero())
				require.False(t, stored.SlotReleased)
				require.False(t, stored.Accounted())
				require.Empty(t, accountant.all())
				require.Zero(t, runner.polls)
				reopened, err := jobs.NewService(records, jobs.WithJobMeter(meter), jobs.WithClock(func() time.Time { return now }))
				require.NoError(t, err)
				_, err = reopened.Sweep(ctx)
				require.NoError(t, err)
				total, err := meter.Total(ctx, accountA)
				require.NoError(t, err)
				require.Equal(t, int64(1), total)
				_, err = reopened.Submit(ctx, open(acceptedRunner()), submission)
				require.Error(t, err, "local polling exhaustion cannot grant another provider dispatch")
			})
		})
	}
}
