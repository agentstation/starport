package jobs_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/jobs"
)

// TestPollingBudgetDoesNotChangeProviderState keeps polling policy separate from execution.
func TestPollingBudgetDoesNotChangeProviderState(t *testing.T) {
	t.Parallel()
	policy := jobs.DefaultPollPolicy()
	require.NoError(t, policy.Validate())
	job := newTestJob(t)
	require.NoError(t, job.Transition(jobs.JobStateRunning, submitted.Add(time.Minute)))
	before := job
	require.False(t, policy.Spent(job, submitted.Add(policy.Lifetime-time.Nanosecond)))
	require.True(t, policy.Spent(job, submitted.Add(policy.Lifetime)))
	require.True(t, policy.Spent(job, submitted.Add(10*policy.Lifetime)))
	require.Equal(t, before, job)
}

// TestATerminalJobIsNeverSpent keeps the lifetime from rewriting an answer the
// caller already has. A completed job that outlived the polling budget still
// produced its asset, and moving it to failed would discard work the account
// paid for.
func TestATerminalJobIsNeverSpent(t *testing.T) {
	t.Parallel()

	policy := jobs.DefaultPollPolicy()
	long := submitted.Add(policy.Lifetime * 10)

	for _, terminal := range []jobs.JobState{
		jobs.JobStateCompleted,
		jobs.JobStateFailed,
		jobs.JobStateCancelled,
	} {
		job := newTestJob(t)
		if terminal == jobs.JobStateFailed {
			require.NoError(t, job.Fail("the provider rejected the prompt", submitted.Add(time.Minute)))
		} else {
			require.NoError(t, job.Transition(terminal, submitted.Add(time.Minute)))
		}
		require.Falsef(t, policy.Spent(job, long), "%s was spent", terminal)
		require.Equalf(t, terminal, job.State, "%s changed", terminal)
	}
}

// TestTheBackoffGrowsAndStops holds the second bound. A fixed short interval
// would spend one provider request per second for the whole of a job that takes
// minutes, and an interval that grew without a ceiling would leave a finished
// job uncollected for longer than the work took.
func TestTheBackoffGrowsAndStops(t *testing.T) {
	t.Parallel()

	policy := jobs.DefaultPollPolicy()
	require.Equal(t, 2*time.Second, policy.Backoff(0))
	require.Equal(t, 4*time.Second, policy.Backoff(1))
	require.Equal(t, 8*time.Second, policy.Backoff(2))
	require.Equal(t, 16*time.Second, policy.Backoff(3))
	require.Equal(t, policy.Max, policy.Backoff(4))
	require.Equal(t, policy.Max, policy.Backoff(400))

	// The requests a full lifetime costs are what the backoff buys. A fixed
	// first interval would cost the lifetime divided by that interval.
	polls, elapsed := 0, time.Duration(0)
	for elapsed < policy.Lifetime {
		elapsed += policy.Backoff(polls)
		polls++
	}
	require.Less(t, polls, 130)
	require.Greater(t, polls, int(policy.Lifetime/policy.Max)-2)
}

// TestAPolicyThatWouldNotTerminateIsRefused keeps an operator from configuring
// a bound that never stops a job.
func TestAPolicyThatWouldNotTerminateIsRefused(t *testing.T) {
	t.Parallel()

	require.NoError(t, jobs.DefaultPollPolicy().Validate())
	for _, broken := range []jobs.PollPolicy{
		{First: 0, Max: time.Second, Lifetime: time.Hour},
		{First: time.Minute, Max: time.Second, Lifetime: time.Hour},
		{First: time.Second, Max: time.Minute, Lifetime: 0},
	} {
		require.Error(t, broken.Validate())
	}
}
