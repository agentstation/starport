package jobs_test

import (
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestCancellationRetainsProviderOutcomeAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report jobs.Report
		err    error
	}{
		{name: "queued", report: jobs.Report{State: jobs.JobStateQueued}},
		{name: "running", report: jobs.Report{State: jobs.JobStateRunning}},
		{name: "completed", report: jobs.Report{State: jobs.JobStateCompleted}},
		{name: "cancelled", report: jobs.Report{State: jobs.JobStateCancelled}},
		{name: "failed", report: jobs.Report{State: jobs.JobStateFailed, Reason: "provider failure"}},
		{name: "missing state"},
		{name: "unknown state", report: jobs.Report{State: "unrecognized"}},
		{name: "lost response", err: errors.New("response lost")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
				ctx := t.Context()
				records, err := jobs.OpenRepository(backing)
				require.NoError(t, err)
				meter, err := jobslots.Open(backing)
				require.NoError(t, err)
				service, err := jobs.NewService(records, jobs.WithJobMeter(meter))
				require.NoError(t, err)
				runner := acceptedRunner()
				request := submissionFor(accountA)
				request.OutstandingBound = 1
				job, err := service.Submit(ctx, open(runner), request)
				require.NoError(t, err)
				runner.cancel, runner.cancelErr = tc.report, tc.err
				_, err = service.Cancel(ctx, runner, accountA, job.ID)
				invalid := tc.err != nil || !tc.report.State.Valid()
				if invalid {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				// Reopen the service before inspecting durable state and capacity.
				reopened, err := jobs.NewService(records, jobs.WithJobMeter(meter))
				require.NoError(t, err)
				stored, err := reopened.Get(ctx, accountA, job.ID)
				require.NoError(t, err)
				state := tc.report.State
				if invalid {
					state = jobs.JobStateQueued
				}
				require.Equal(t, state, stored.State)
				require.Equal(t, state.Terminal(), stored.SlotReleased)
				total, err := meter.Total(ctx, accountA)
				require.NoError(t, err)
				if state.Terminal() {
					require.Zero(t, total)
					return
				}
				require.Equal(t, int64(1), total)
				_, err = reopened.Submit(ctx, open(acceptedRunner()), request)
				require.Error(t, err, "unconfirmed work must retain its capacity")
				runner.poll = jobs.Report{State: jobs.JobStateCompleted}
				resolved, err := reopened.Refresh(ctx, runner, accountA, job.ID)
				require.NoError(t, err)
				require.Equal(t, jobs.JobStateCompleted, resolved.State)
				require.True(t, resolved.SlotReleased)
				total, err = meter.Total(ctx, accountA)
				require.NoError(t, err)
				require.Zero(t, total)
			})
		})
	}
}
