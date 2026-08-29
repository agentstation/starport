package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/routing"
)

// recordingAccountant is the priced side under test. It keeps every entry it is
// given rather than a count, because the property under test is that exactly
// one entry exists and that the one entry says the right thing. A counter would
// pass a service that reported the same job twice with different words.
type recordingAccountant struct {
	mu      sync.Mutex
	entries []jobs.AccountingEntry
	err     error
}

func (a *recordingAccountant) RecordJob(_ context.Context, entry jobs.AccountingEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, entry)
	return a.err
}

func (a *recordingAccountant) all() []jobs.AccountingEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]jobs.AccountingEntry(nil), a.entries...)
}

// countingMeter stands in for the limits meter. It counts claims and releases
// separately, because a slot the service forgot to give back and a slot it gave
// back twice are different defects and a net total hides both.
type countingMeter struct {
	mu       sync.Mutex
	reserves int
	releases int
	holders  []string
	refuse   error
}

func (m *countingMeter) Reserve(_ context.Context, holder string, _, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refuse != nil {
		return m.refuse
	}
	m.reserves++
	m.holders = append(m.holders, holder)
	return nil
}

func (m *countingMeter) Release(_ context.Context, holder string, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases++
	m.holders = append(m.holders, holder)
	return nil
}

func (m *countingMeter) counts() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reserves, m.releases
}

// newAccountedService builds a service whose accounting and metering are both
// observable.
func newAccountedService(t *testing.T) (*jobs.Service, *recordingAccountant, *countingMeter) {
	t.Helper()
	accountant := &recordingAccountant{}
	meter := &countingMeter{}
	service, _ := newService(t, jobs.WithAccountant(accountant), jobs.WithJobMeter(meter))
	return service, accountant, meter
}

// TestACompletedJobDrawsOneRecordHoweverOftenACallerPolls is the whole point of
// stamping the record rather than counting in the accounting seam.
//
// A caller polls until it sees a terminal answer, and a client that retries or
// a browser tab left open polls well past that. Every one of those reads is a
// read of the same finished work. A per-read charge would bill an account for
// asking how much it had already been billed.
func TestACompletedJobDrawsOneRecordHoweverOftenACallerPolls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, accountant, _ := newAccountedService(t)
	runner := acceptedRunner()
	runner.poll = jobs.Report{State: jobs.JobStateCompleted}

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	require.Empty(t, accountant.all(), "a queued job has not ended and prices nothing")

	for range 10 {
		polled, err := service.Refresh(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, jobs.JobStateCompleted, polled.State)
		require.True(t, polled.Accounted())
	}

	entries := accountant.all()
	require.Len(t, entries, 1)
	require.Equal(t, job.ID, entries[0].JobID)
	require.Equal(t, accountA, entries[0].Account)
	require.Equal(t, jobs.JobStateCompleted, entries[0].State)
	require.True(t, entries[0].Chargeable)
	require.Equal(t, "deepinfra", entries[0].Provider)
	require.Equal(t, "deepinfra/wan-2.2", entries[0].Model)
}

// TestAFailedJobDrawsNoCost states the rule a caller cares about most. The
// provider produced nothing, so the account owes nothing, and the entry says so
// in the flag rather than by being absent.
func TestAFailedJobDrawsNoCost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, accountant, _ := newAccountedService(t)
	runner := acceptedRunner()
	runner.poll = jobs.Report{State: jobs.JobStateFailed, Reason: "the provider rejected the prompt"}

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	failed, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateFailed, failed.State)

	entries := accountant.all()
	require.Len(t, entries, 1)
	require.Equal(t, jobs.JobStateFailed, entries[0].State)
	require.False(t, entries[0].Chargeable)
}

// TestACancelledJobDrawsNoCost separates the two free ends. A caller that
// stopped its own work did not fail, and an account reading its history needs
// to tell the two apart even though neither costs.
func TestACancelledJobDrawsNoCost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, accountant, _ := newAccountedService(t)
	runner := acceptedRunner()

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	stopped, err := service.Cancel(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCancelled, stopped.State)

	entries := accountant.all()
	require.Len(t, entries, 1)
	require.Equal(t, jobs.JobStateCancelled, entries[0].State)
	require.False(t, entries[0].Chargeable)
}

// TestATerminalJobFreesOneSlot is what keeps the outstanding job limit from
// becoming a lifetime quota. A slot claimed at submit and never given back
// would let an account submit its bound once and never again.
func TestATerminalJobFreesOneSlot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _, meter := newAccountedService(t)
	runner := acceptedRunner()
	runner.poll = jobs.Report{State: jobs.JobStateCompleted}

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	reserves, releases := meter.counts()
	require.Equal(t, 1, reserves)
	require.Equal(t, 0, releases, "a queued job holds the slot it claimed")

	// Poll past the terminal answer. The slot comes back once, on the same
	// stamp that draws the one usage record.
	for range 3 {
		_, err := service.Refresh(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
	}
	reserves, releases = meter.counts()
	require.Equal(t, 1, reserves)
	require.Equal(t, 1, releases)
}

// TestASubmissionOverTheLimitReachesNoProvider is why the claim happens before
// the runner is built. Building one resolves a route and a credential, so a
// refusal that had already done that would bound what an account reads rather
// than what it pays for, which is the opposite of what the limit is for.
func TestASubmissionOverTheLimitReachesNoProvider(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	accountant := &recordingAccountant{}
	refused := errors.New("outstanding job limit exceeded")
	meter := &countingMeter{refuse: refused}
	service, records := newService(t, jobs.WithAccountant(accountant), jobs.WithJobMeter(meter))
	runner := acceptedRunner()
	built := 0

	_, err := service.Submit(ctx, func(context.Context) (jobs.Runner, error) {
		built++
		return runner, nil
	}, jobs.Submission{
		Account:          accountA,
		Operation:        routing.OperationVideosGenerations,
		OutstandingBound: 1,
	})
	require.ErrorIs(t, err, refused)
	require.Zero(t, built, "a refused submission resolves no route and no credential")
	require.Zero(t, runner.submits, "a refused submission spends no provider work")

	_, err = records.Get(ctx, accountA, "job_service_01")
	require.ErrorIs(t, err, jobs.ErrJobNotFound)
}

// TestARefusedProviderGivesTheSlotBack covers the path between the two: the
// claim succeeded and the work never started. A slot leaked here would be the
// worst kind, because a provider outage would walk an account into its own
// limit and keep it there.
func TestARefusedProviderGivesTheSlotBack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _, meter := newAccountedService(t)
	runner := acceptedRunner()
	runner.submitErr = errors.New("the provider refused the prompt")

	_, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.Error(t, err)

	reserves, releases := meter.counts()
	require.Equal(t, 1, reserves)
	require.Equal(t, 1, releases)
}

// TestTheSweepClosesAJobNobodyCameBackFor is the other half of the bound. Every
// other path settles a job because a caller polled it, and a caller that
// submits and walks away is exactly the caller the limit exists for. Without
// this pass one abandoned job holds one slot for as long as the process runs.
func TestTheSweepClosesAJobNobodyCameBackFor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	accountant := &recordingAccountant{}
	meter := &countingMeter{}
	clock := &assetClock{now: submitted}
	service, _ := newService(t,
		jobs.WithAccountant(accountant),
		jobs.WithJobMeter(meter),
		jobs.WithClock(clock.read))
	runner := acceptedRunner()

	_, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	// Move past the polling budget. A provider that has not answered by then is
	// not going to, and nothing is polling this job to notice.
	clock.now = submitted.Add(jobs.DefaultLifetime + time.Minute)
	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Abandoned)
	require.Equal(t, 1, result.Accounted)

	entries := accountant.all()
	require.Len(t, entries, 1)
	require.Equal(t, jobs.JobStateFailed, entries[0].State)
	require.False(t, entries[0].Chargeable)

	reserves, releases := meter.counts()
	require.Equal(t, 1, reserves)
	require.Equal(t, 1, releases)

	// A second pass finds nothing. The stamp is what stops it, so a sweep on a
	// ticker does not draw a record per tick for the rest of the day.
	again, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Zero(t, again.Abandoned)
	require.Zero(t, again.Accounted)
	require.Len(t, accountant.all(), 1)
}
