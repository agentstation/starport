package jobs_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type assetFetcher func(context.Context, string, int64) (jobs.Asset, error)

func (f assetFetcher) Fetch(ctx context.Context, reference string, bound int64) (jobs.Asset, error) {
	return f(ctx, reference, bound)
}

func TestNativeExternalRecoveryPreservesBilling(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(func() time.Time { return submitted }), jobs.WithRetention(time.Hour), jobs.WithAssetBound(4096))
		require.NoError(t, err)
		runner := nativeFixture()
		runner.asset = jobs.Asset{}
		runner.assetURL = "https://asset.example/video?signature=private"
		job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.NoError(t, err)
		require.Equal(t, "blocked", job.AssetStatus(submitted))
		require.True(t, job.Accounted(), "asset permissions do not block usage reporting")
		require.Equal(t, runner.measurement.Quantities, job.Measurement.Quantities)
		calls := 0
		fetcher := assetFetcher(func(ctx context.Context, ref string, bound int64) (jobs.Asset, error) {
			calls++
			require.Equal(t, runner.assetURL, ref)
			require.Equal(t, int64(4096), bound, "recovery retains the submitted bound")
			if calls == 1 {
				return jobs.Asset{}, jobs.ErrAssetDownloadUnavailable
			}
			return jobs.Asset{ContentType: "video/mp4", Bytes: []byte("video")}, nil
		})
		reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithExternalAssets(fetcher), jobs.WithClock(func() time.Time { return submitted.Add(time.Minute) }), jobs.WithAssetBound(1), jobs.WithRetention(time.Minute))
		require.NoError(t, err)
		job, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, "retry", job.AssetStatus(submitted))
		job, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, "stored", job.AssetStatus(submitted))
		require.Equal(t, submitted.Add(time.Hour), job.AssetExpiresAt)
		_, reader, err := reopened.Open(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, []byte("video"), data)
		_, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, 2, calls)
		require.Equal(t, 1, runner.submits)
		require.Zero(t, runner.polls)
	})
}

func TestNativeDownloadCannotExtendRetention(t *testing.T) {
	service, records, assets, clock := newAssetService(t)
	runner := nativeFixture()
	runner.asset = jobs.Asset{}
	runner.assetURL = "https://asset.example/video"
	job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.NoError(t, err)
	calls := 0
	fetcher := assetFetcher(func(context.Context, string, int64) (jobs.Asset, error) {
		calls++
		clock.now = submitted.Add(retentionWindow)
		return jobs.Asset{ContentType: "video/mp4", Bytes: []byte("late")}, nil
	})
	reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithExternalAssets(fetcher), jobs.WithClock(clock.read))
	require.NoError(t, err)
	job, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, "expired", job.AssetStatus(clock.now))
	require.False(t, job.HasAsset())
	_, _, err = reopened.Open(t.Context(), accountA, job.ID)
	require.ErrorIs(t, err, jobs.ErrAssetExpired)
	_, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, runner.submits)
}

func TestNativeSweepSettlesDespiteAssetWriteFailure(t *testing.T) {
	_, records, assets, clock := newAssetService(t)
	blobs := &interruptedNativeAssets{Store: assets, failAt: 2}
	service, err := jobs.NewService(records, jobs.WithAssetStore(blobs), jobs.WithClock(clock.read))
	require.NoError(t, err)
	runner := nativeFixture()
	job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.Error(t, err)
	// Force the sweep's next asset write to fail as well.
	blobs.failAt = blobs.puts + 1
	result, err := service.Sweep(t.Context())
	require.Error(t, err)
	require.Equal(t, 1, result.Failed)
	retained, err := records.Get(t.Context(), accountA, job.ID)
	require.NoError(t, err)
	require.True(t, retained.Accounted())
	require.Equal(t, runner.measurement.Quantities, retained.Measurement.Quantities)
}

func TestNativeRecoveryRejectsChangedAssetBytes(t *testing.T) {
	_, records, assets, clock := newAssetService(t)
	blobs := &interruptedNativeAssets{Store: assets, failAt: 2}
	calls := 0
	fetcher := assetFetcher(func(context.Context, string, int64) (jobs.Asset, error) {
		calls++
		if calls == 1 {
			return jobs.Asset{ContentType: "video/mp4", Bytes: []byte("first")}, nil
		}
		return jobs.Asset{ContentType: "video/mp4", Bytes: []byte("different")}, nil
	})
	service, err := jobs.NewService(records, jobs.WithAssetStore(blobs), jobs.WithExternalAssets(fetcher), jobs.WithClock(clock.read))
	require.NoError(t, err)
	runner := nativeFixture()
	runner.asset = jobs.Asset{}
	runner.assetURL = "https://asset.example/video"
	job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.Error(t, err)
	job, err = service.Refresh(t.Context(), runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, "invalid", job.AssetStatus(clock.now))
	require.False(t, job.HasAsset())
	require.True(t, job.Accounted())
	require.Equal(t, 1, runner.submits)
	require.False(t, errors.Is(err, jobs.ErrSubmissionUnconfirmed))
}

func TestNativeConcurrentReplicasBindOneAsset(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		initial, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(func() time.Time { return submitted }))
		require.NoError(t, err)
		runner := nativeFixture()
		runner.asset = jobs.Asset{}
		runner.assetURL = "https://assets.example/video"
		job, err := initial.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.NoError(t, err)
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		done := make(chan error, 2)
		for _, body := range []string{"first", "second"} {
			fetcher := assetFetcher(func(ctx context.Context, _ string, _ int64) (jobs.Asset, error) {
				entered <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return jobs.Asset{}, ctx.Err()
				}
				return jobs.Asset{ContentType: "video/mp4", Bytes: []byte(body)}, nil
			})
			replica, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithExternalAssets(fetcher), jobs.WithClock(func() time.Time { return submitted }))
			require.NoError(t, err)
			go func() { _, err := replica.Refresh(t.Context(), runner, accountA, job.ID); done <- err }()
		}
		<-entered
		<-entered
		close(release)
		for range 2 {
			err := <-done
			if err != nil {
				require.ErrorIs(t, err, storage.ErrConflict)
			}
		}
		stored, reader, err := initial.Open(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Contains(t, []string{"first", "second"}, string(data))
		require.EqualValues(t, len(data), stored.AssetBytes)
		require.Equal(t, "stored", stored.AssetStatus(submitted))
		require.Equal(t, 1, runner.submits)
	})
}

type canceledAfterRead struct {
	jobs.Repository
	target string
	read   chan struct{}
}

func (r canceledAfterRead) Get(ctx context.Context, account, id string) (jobs.Job, error) {
	job, err := r.Repository.Get(ctx, account, id)
	if id == r.target && err == nil {
		close(r.read)
		<-ctx.Done()
	}
	return job, err
}

func TestNativeRecoveryBoundsTransfers(t *testing.T) {
	_, records, assets, clock := newAssetService(t)
	service, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(clock.read))
	require.NoError(t, err)
	runner := nativeFixture()
	runner.asset = jobs.Asset{}
	runner.assetURL = "https://assets.example/video"
	first, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.NoError(t, err)
	second, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.NoError(t, err)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	fetcher := assetFetcher(func(ctx context.Context, _ string, _ int64) (jobs.Asset, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return jobs.Asset{}, ctx.Err()
		}
		return jobs.Asset{ContentType: "video/mp4", Bytes: []byte("video")}, nil
	})
	read := make(chan struct{})
	replica, err := jobs.NewService(canceledAfterRead{records, second.ID, read}, jobs.WithAssetStore(assets), jobs.WithExternalAssets(fetcher), jobs.WithClock(clock.read), jobs.WithWorkers(1, time.Minute))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := replica.Refresh(t.Context(), runner, accountA, first.ID); done <- err }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	waiting := make(chan error, 1)
	go func() { _, err := replica.Refresh(ctx, runner, accountA, second.ID); waiting <- err }()
	<-read
	cancel()
	require.ErrorIs(t, <-waiting, context.Canceled)
	require.Empty(t, entered, "a waiting recovery must not start another transfer")
	close(release)
	require.NoError(t, <-done)
	require.Equal(t, 2, runner.submits)
}
