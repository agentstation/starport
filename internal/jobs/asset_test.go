package jobs_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
)

// assetBound is the bound these tests hand the service. It is small enough to
// state in a line and large enough for the bytes below, and it appears in an
// assertion: the bound the record store holds is what reaches the provider
// side, and a service that read a bound of its own would not be caught by a
// test that never looked.
const assetBound int64 = 4096

// retentionWindow is short so a test can pass it by moving a clock rather than
// by waiting.
const retentionWindow = time.Hour

// errProviderUnreachable stands for any failure at the fetch. The rule under
// test does not read the reason.
var errProviderUnreachable = errors.New("the provider was unreachable")

// assetClock is a clock a test moves. The retention rule is entirely about
// time, so the only way to state it is to control the reading.
type assetClock struct {
	now time.Time
}

func (c *assetClock) read() time.Time { return c.now }

// newAssetService builds a service with somewhere to put bytes. Every other
// test in this package builds one without a byte store, which is what makes the
// asset path opt-in rather than something a deployment gets by accident.
func newAssetService(t *testing.T) (*jobs.Service, jobs.Repository, blob.Store, *assetClock) {
	t.Helper()
	store, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	clock := &assetClock{now: submitted}
	service, records := newService(t,
		jobs.WithClock(clock.read),
		jobs.WithAssetStore(store),
		jobs.WithRetention(retentionWindow),
		jobs.WithAssetBound(assetBound))
	return service, records, store, clock
}

// finishingRunner accepts a job as queued and reports it completed on the first
// poll, with bytes waiting.
func finishingRunner() *recordingRunner {
	runner := acceptedRunner()
	runner.poll = jobs.Report{State: jobs.JobStateCompleted}
	runner.asset = jobs.Asset{ContentType: "video/mp4", Bytes: []byte("finished-video-bytes")}
	return runner
}

// TestAFinishedJobFetchesItsAssetOnce is the property the whole retention rule
// rests on. A caller polls until it sees a terminal answer and usually once
// more after that. Each of those reads has to cost no second provider request,
// no second use of the account's credential, and no second stored copy.
func TestAFinishedJobFetchesItsAssetOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _, store, _ := newAssetService(t)
	runner := finishingRunner()

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	finished, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCompleted, finished.State)
	require.NotEmpty(t, finished.AssetKey)
	require.Equal(t, "video/mp4", finished.AssetContentType)
	require.Equal(t, int64(len(runner.asset.Bytes)), finished.AssetBytes)
	require.Equal(t, submitted.Add(retentionWindow), finished.AssetExpiresAt)

	// The bound travels to the provider side as an argument. Without this the
	// connector would be free to read a setting of its own, and the half that
	// stores the bytes would not be the half that decided how large they may be.
	require.Equal(t, []int64{assetBound}, runner.bounds)

	for range 5 {
		polled, err := service.Refresh(ctx, runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, finished.AssetKey, polled.AssetKey)
	}
	require.Equal(t, 1, runner.fetches)

	// One key, one object. A second fetch would have written a second one and
	// left the first unreachable.
	stored, reader, err := service.Open(ctx, accountA, job.ID)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	bytes, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, runner.asset.Bytes, bytes)
	require.Equal(t, stored.AssetKey, finished.AssetKey)

	info, err := store.Stat(ctx, finished.AssetKey)
	require.NoError(t, err)
	require.Equal(t, finished.AssetBytes, info.Size)
}

// TestAFailedFetchLeavesACompletedJobAndRetries states what a provider that is
// briefly unreachable costs. The work completed, so reporting a failure to the
// caller would name the wrong thing. The record stays truthful, the content
// route answers that it holds nothing, and the next read tries again.
func TestAFailedFetchLeavesACompletedJobAndRetries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _, _, _ := newAssetService(t)
	runner := finishingRunner()
	runner.fetchErr = errProviderUnreachable

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)

	finished, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCompleted, finished.State)
	require.Empty(t, finished.AssetKey)
	require.Equal(t, 1, runner.fetches)

	_, _, err = service.Open(ctx, accountA, job.ID)
	require.ErrorIs(t, err, jobs.ErrAssetNotFound)

	runner.fetchErr = nil
	collected, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.NotEmpty(t, collected.AssetKey)
	require.Equal(t, 2, runner.fetches)
}

// TestTheSweepDeletesAnAssetPastItsWindow is the retention rule. The bytes go
// and the record stays: a completed job stays completed after its asset
// expires, because the work happened and the account paid for it. The expired
// marker is what separates that from a job that never produced an asset.
func TestTheSweepDeletesAnAssetPastItsWindow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, records, store, clock := newAssetService(t)
	runner := finishingRunner()

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	finished, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.True(t, finished.HasAsset())

	// One second inside the window reclaims nothing. Without this half, a sweep
	// that deleted every asset it saw would pass the half below.
	clock.now = finished.AssetExpiresAt.Add(-time.Second)
	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Expired)
	_, err = store.Stat(ctx, finished.AssetKey)
	require.NoError(t, err)

	clock.now = finished.AssetExpiresAt.Add(time.Second)
	result, err = service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Expired)

	expired, err := records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCompleted, expired.State)
	require.Equal(t, clock.now, expired.AssetExpiredAt)
	require.False(t, expired.HasAsset())

	_, err = store.Stat(ctx, finished.AssetKey)
	require.ErrorIs(t, err, blob.ErrNotFound)

	// A second pass finds nothing to do. The marker is what stops it, and a
	// sweep that ran on a ticker would otherwise repeat a delete every tick.
	result, err = service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Expired)
}

// TestAnAssetPastItsWindowIsRefusedBeforeTheSweepRuns states why expiry is
// decided on the read. The sweep runs on an interval. An asset that answered
// for the length of that interval past its stated window would make the window
// a suggestion.
func TestAnAssetPastItsWindowIsRefusedBeforeTheSweepRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _, store, clock := newAssetService(t)
	runner := finishingRunner()

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	finished, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)

	clock.now = finished.AssetExpiresAt.Add(time.Second)
	expired, reader, err := service.Open(ctx, accountA, job.ID)
	require.ErrorIs(t, err, jobs.ErrAssetExpired)
	require.Nil(t, reader)
	require.False(t, expired.AssetExpiredAt.IsZero())

	// The read reclaimed the bytes rather than only refusing to serve them.
	_, err = store.Stat(ctx, finished.AssetKey)
	require.ErrorIs(t, err, blob.ErrNotFound)
}

// TestAJobKeepsTheWindowItWasPromised is why the record stores the end of the
// window rather than computing it. An operator who shortens the setting is
// stating what new jobs get. A job already stored under a longer promise keeps
// it, and one already stored under a shorter one does not gain time.
func TestAJobKeepsTheWindowItWasPromised(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, records, _, _ := newAssetService(t)
	runner := finishingRunner()

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	finished, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)

	stored, err := records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, finished.AssetExpiresAt, stored.AssetExpiresAt)
	require.Equal(t, retentionWindow, service.Retention())
}

// TestAJobWithNoByteStoreCollectsNothing keeps the asset path opt-in. A
// deployment that assembled no byte store answers about the job it ran rather
// than failing on a fetch it has nowhere to put.
func TestAJobWithNoByteStoreCollectsNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newService(t)
	runner := finishingRunner()

	job, err := service.Submit(ctx, open(runner), submissionFor(accountA))
	require.NoError(t, err)
	finished, err := service.Refresh(ctx, runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCompleted, finished.State)
	require.Empty(t, finished.AssetKey)
	require.Equal(t, 0, runner.fetches)

	result, err := service.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.Expired)
}
