package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/routing"
)

// recordingRunner is one provider side under test. It counts every call, which
// is what several of these tests actually assert: the number of provider
// requests a caller's behaviour costs is the property this service exists to
// bound, and a count is the only way to state it.
type recordingRunner struct {
	acceptance jobs.Acceptance
	submitErr  error
	onSubmit   func()

	poll    jobs.Report
	pollErr error

	cancel    jobs.Report
	cancelErr error

	asset    jobs.Asset
	fetchErr error

	submits int
	polls   int
	cancels int
	fetches int
	bounds  []int64
	handles []jobs.Handle
}

// open hands the service a runner it already holds. Production builds the
// runner inside the call, because that is where the route and the credential
// are resolved and both come after the account's bound is claimed.
func open(runner jobs.Runner) jobs.OpenRunner {
	return func(context.Context) (jobs.Runner, error) { return runner, nil }
}

// submissionFor is what every test that does not test the limit submits: one
// account, no key, and no bound. A test that means to exercise the bound states
// its own.
func submissionFor(account string) jobs.Submission {
	return jobs.Submission{Account: account, Operation: routing.OperationVideosGenerations}
}

func (r *recordingRunner) Submit(ctx context.Context, recorder jobs.SubmissionRecorder) (jobs.Acceptance, error) {
	if err := recorder.BeforeDispatch(ctx, jobs.Dispatch{Provider: r.acceptance.Provider, Model: r.acceptance.Model, CatalogGeneration: "test-generation"}); err != nil {
		return jobs.Acceptance{}, err
	}
	r.submits++
	if r.onSubmit != nil {
		r.onSubmit()
	}
	if r.submitErr != nil {
		return jobs.Acceptance{}, r.submitErr
	}
	if err := recorder.Accepted(ctx, r.acceptance); err != nil {
		return jobs.Acceptance{}, err
	}
	return r.acceptance, nil
}

func (r *recordingRunner) Poll(_ context.Context, handle jobs.Handle) (jobs.Report, error) {
	r.polls++
	r.handles = append(r.handles, handle)
	return r.poll, r.pollErr
}

func (r *recordingRunner) Cancel(_ context.Context, handle jobs.Handle) (jobs.Report, error) {
	r.cancels++
	r.handles = append(r.handles, handle)
	return r.cancel, r.cancelErr
}

func (r *recordingRunner) Fetch(
	_ context.Context,
	handle jobs.Handle,
	maxBytes int64,
) (jobs.Asset, error) {
	r.fetches++
	r.bounds = append(r.bounds, maxBytes)
	r.handles = append(r.handles, handle)
	return r.asset, r.fetchErr
}

func acceptedRunner() *recordingRunner {
	return &recordingRunner{cancel: jobs.Report{State: jobs.JobStateCancelled}, acceptance: jobs.Acceptance{
		Provider:      "deepinfra",
		Model:         "deepinfra/wan-2.2",
		ProviderJobID: "provider-side-identifier",
		State:         jobs.JobStateQueued,
	}}
}

func newService(t *testing.T, options ...jobs.ServiceOption) (*jobs.Service, jobs.Repository) {
	t.Helper()
	records, _ := newRepository(t)
	fixed := append([]jobs.ServiceOption{
		jobs.WithClock(func() time.Time { return submitted }),
		jobs.WithIdentifiers(func() string { return "job_service_01" }),
	}, options...)
	service, err := jobs.NewService(records, fixed...)
	require.NoError(t, err)
	return service, records
}

// TestSubmitRecordsWhatTheProviderAccepted states the whole submit contract.
// The record names the provider and the model the route resolved to, not what
// the caller asked for, because the caller names a catalog model and only the
// route knows which provider served it.
func TestSubmitRecordsWhatTheProviderAccepted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, records := newService(t)
	runner := acceptedRunner()

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	require.Equal(t, 1, runner.submits)
	require.Equal(t, "job_service_01", job.ID)
	require.Equal(t, "deepinfra", job.Provider)
	require.Equal(t, "deepinfra/wan-2.2", job.Model)
	require.Equal(t, jobs.JobStateQueued, job.State)
	require.True(t, job.HasProviderJob())

	// The record survives the call, which is what makes the identifier the
	// caller holds worth anything after the response closes.
	stored, err := records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, job.Provider, stored.Provider)
	require.True(t, stored.HasProviderJob())
}

// A provider error does not prove that it refused the submitted work.
func TestSubmitRetainsRecordWhenProviderReplyFails(t *testing.T) {
	t.Parallel()
	service, records := newService(t)
	runner := acceptedRunner()
	runner.submitErr = errors.New("provider response unavailable")
	_, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
	job, err := records.Get(t.Context(), accountA, "job_service_01")
	require.NoError(t, err)
	require.True(t, job.SubmissionPending)
	require.False(t, job.HasProviderJob())
}

// TestSubmitRefusesAJobThatNamesNoAccount keeps an unowned record out of the
// store. A record with no account is readable by the first caller that guesses
// its identifier, because every read is scoped by account.
func TestSubmitRefusesAJobThatNamesNoAccount(t *testing.T) {
	t.Parallel()

	service, _ := newService(t)
	_, err := service.Submit(context.Background(), open(acceptedRunner()), submissionFor("  "))
	require.ErrorIs(t, err, jobs.ErrInvalidJob)
}

// TestPollingAFinishedJobReachesNoProvider is the rule the accounting rule
// rests on. A caller polls until it sees a terminal answer and often once more
// after that, and every one of those reads has to be free: free of a provider
// request, free of the account's credential, and free of a second cost record.
func TestPollingAFinishedJobReachesNoProvider(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newService(t)
	runner := acceptedRunner()
	runner.acceptance.State = jobs.JobStateCompleted

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCompleted, job.State)

	for range 5 {
		refreshed, err := service.Refresh(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, jobs.JobStateCompleted, refreshed.State)
	}
	require.Zero(t, runner.polls)
}

// TestRefreshAdvancesTheRecordFromTheProviderAnswer covers the ordinary poll.
// The handle carries the provider identifier the record holds, which is the one
// path by which that value leaves this package.
func TestRefreshAdvancesTheRecordFromTheProviderAnswer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, records := newService(t)
	runner := acceptedRunner()
	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	runner.poll = jobs.Report{State: jobs.JobStateRunning}
	refreshed, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateRunning, refreshed.State)
	require.Equal(t, 1, runner.polls)
	require.Len(t, runner.handles, 1)
	require.Equal(t, "provider-side-identifier", runner.handles[0].ProviderJobID)
	require.Equal(t, "deepinfra", runner.handles[0].Provider)

	stored, err := records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateRunning, stored.State)
}

// TestRefreshOfAnUnchangedJobRewritesNothing keeps a poll that learned nothing
// from spending a storage write. A running job that a caller polls every two
// seconds for an hour is the ordinary case, not the rare one.
func TestRefreshOfAnUnchangedJobRewritesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newService(t)
	runner := acceptedRunner()
	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	runner.poll = jobs.Report{State: jobs.JobStateQueued}
	refreshed, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateQueued, refreshed.State)
	require.True(t, refreshed.TerminalAt.IsZero())
}

// TestAFailedProviderAnswerAlwaysStatesAReason covers the state a caller cannot
// act on without one. A provider that reports a failure and says nothing still
// produces a record that explains itself.
func TestAFailedProviderAnswerAlwaysStatesAReason(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newService(t)
	runner := acceptedRunner()
	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	runner.poll = jobs.Report{State: jobs.JobStateFailed}
	failed, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateFailed, failed.State)
	require.NotEmpty(t, failed.Reason)
	require.False(t, failed.TerminalAt.IsZero())
}

// TestASpentJobRetainsStateWithoutAskingTheProvider bounds automatic polling.
func TestASpentJobRetainsStateWithoutAskingTheProvider(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clock := submitted
	service, _ := newService(t, jobs.WithClock(func() time.Time { return clock }))
	runner := acceptedRunner()
	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	clock = submitted.Add(jobs.DefaultLifetime + time.Minute)
	spent, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateQueued, spent.State)
	require.Empty(t, spent.Reason)
	require.True(t, service.NeedsReconciliation(spent))
	require.Zero(t, runner.polls)
}

// TestRefreshOfAnotherAccountsJobIsNotFound is the isolation rule at the service
// rather than at the store. A read that reached the provider before it checked
// the owner would spend the wrong account's credential.
func TestRefreshOfAnotherAccountsJobIsNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newService(t)
	runner := acceptedRunner()
	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	_, err = service.Refresh(ctx, runner, accountB, job.ID)
	require.ErrorIs(t, err, jobs.ErrJobNotFound)
	require.Zero(t, runner.polls)

	_, err = service.Cancel(ctx, runner, accountB, job.ID)
	require.ErrorIs(t, err, jobs.ErrJobNotFound)
	require.Zero(t, runner.cancels)
}

// TestCancelStopsTheProviderAndTheRecord persists a confirmed cancellation.
func TestCancelStopsTheProviderAndTheRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, records := newService(t)
	runner := acceptedRunner()
	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	runner.cancel = jobs.Report{State: jobs.JobStateCancelled}
	cancelled, err := service.Cancel(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, 1, runner.cancels)
	require.Equal(t, jobs.JobStateCancelled, cancelled.State)
	require.False(t, cancelled.TerminalAt.IsZero())

	stored, err := records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCancelled, stored.State)
}

// TestCancelOfAFinishedJobIsAConflict protects the answer an account paid for. A
// cancellation that rewrote a completed job would discard both the asset and
// the cost record that goes with it.
func TestCancelOfAFinishedJobIsAConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newService(t)
	runner := acceptedRunner()
	runner.acceptance.State = jobs.JobStateCompleted
	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	ended, err := service.Cancel(ctx, runner, accountA, job.ID)
	require.ErrorIs(t, err, jobs.ErrJobAlreadyEnded)
	require.Equal(t, jobs.JobStateCompleted, ended.State)
	require.Zero(t, runner.cancels)
}

// TestTheServiceRefusesACallWithNoProviderSide keeps a job from looking started
// when nothing was asked to start it.
func TestTheServiceRefusesACallWithNoProviderSide(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newService(t)

	_, err := service.Submit(ctx, nil, submissionFor(accountA))
	require.ErrorIs(t, err, jobs.ErrRunnerRequired)

	// A call that names a builder returning nothing reads the same way. The
	// builder runs after the slot is claimed, so this path also proves the
	// claim comes back.
	_, err = service.Submit(ctx, open(nil), submissionFor(accountA))
	require.ErrorIs(t, err, jobs.ErrRunnerRequired)

	job, err := service.Submit(ctx, open(acceptedRunner()), submissionFor(accountA))
	require.NoError(t, err)

	_, err = service.Refresh(ctx, nil, accountA, job.ID)
	require.ErrorIs(t, err, jobs.ErrRunnerRequired)
	_, err = service.Cancel(ctx, nil, accountA, job.ID)
	require.ErrorIs(t, err, jobs.ErrRunnerRequired)
}

// TestNewServiceRefusesAPolicyThatNeverTerminates rejects the settings at
// composition rather than at the first poll. An operator reads a startup
// failure; nobody reads a job that polls forever.
func TestNewServiceRefusesAPolicyThatNeverTerminates(t *testing.T) {
	t.Parallel()

	records, _ := newRepository(t)
	_, err := jobs.NewService(records, jobs.WithPollPolicy(jobs.PollPolicy{
		First: 2 * time.Second, Max: time.Minute,
	}))
	require.ErrorIs(t, err, jobs.ErrInvalidJob)

	_, err = jobs.NewService(nil)
	require.ErrorIs(t, err, jobs.ErrRepositoryRequired)
}
