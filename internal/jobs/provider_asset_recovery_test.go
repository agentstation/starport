package jobs_test

import (
	"bytes"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/stretchr/testify/require"
)

func TestProviderAssetLostAcknowledgmentRetainsOneIdentity(t *testing.T) {
	_, records, assets, clock := newAssetService(t)
	interrupted := &interruptedNativeAssets{Store: assets, failAt: 1, after: true}
	service, err := jobs.NewService(records, jobs.WithAssetStore(interrupted), jobs.WithClock(clock.read))
	require.NoError(t, err)
	runner := finishingRunner()
	job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.NoError(t, err)
	job, err = service.Refresh(t.Context(), runner, accountA, job.ID)
	require.NoError(t, err)
	first := interrupted.firstKey
	require.NotEmpty(t, first)
	reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(clock.read))
	require.NoError(t, err)
	job, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, first, job.AssetKey, "a lost reply must not orphan the first stored asset")
	_, reader, err := reopened.Open(t.Context(), accountA, job.ID)
	require.NoError(t, err)
	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, runner.asset.Bytes, content)
	require.Equal(t, 1, runner.fetches)
}

func TestProviderAssetPreparationRecoversAndExpires(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		interrupted := &interruptedNativeAssets{Store: assets, failAt: 1, after: true}
		writer, err := jobs.NewService(records, jobs.WithAssetStore(interrupted), jobs.WithRetention(time.Hour), jobs.WithClock(func() time.Time { return submitted }))
		require.NoError(t, err)
		runner := finishingRunner()
		job, err := writer.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.NoError(t, err)
		job, err = writer.Refresh(t.Context(), runner, accountA, job.ID)
		require.NoError(t, err)
		require.False(t, job.HasAsset())
		require.Equal(t, "pending", job.AssetStatus(submitted))
		require.ErrorIs(t, records.Delete(t.Context(), accountA, job.ID), jobs.ErrAssetRetirementRequired)
		changed := job
		changed.AssetBytes++
		require.ErrorIs(t, records.Replace(t.Context(), job, changed), jobs.ErrInvalidJob)
		reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(func() time.Time { return submitted.Add(time.Minute) }))
		require.NoError(t, err)
		_, err = reopened.Sweep(t.Context())
		require.NoError(t, err)
		recovered, reader, err := reopened.Open(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.True(t, recovered.HasAsset())
		require.Equal(t, interrupted.firstKey, recovered.AssetKey)
		require.Equal(t, 1, runner.fetches)
		cleaner, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(func() time.Time { return submitted.Add(2 * time.Hour) }))
		require.NoError(t, err)
		_, err = cleaner.Sweep(t.Context())
		require.NoError(t, err)
		_, _, err = cleaner.Open(t.Context(), accountA, job.ID)
		require.ErrorIs(t, err, jobs.ErrAssetExpired)
		require.NoError(t, records.Delete(t.Context(), accountA, job.ID))
		_, err = assets.Publish(t.Context(), recovered.AssetKey, bytes.NewReader([]byte("late")))
		require.ErrorIs(t, err, blob.ErrPublicationExists)
	})
}

func TestProviderRetirementFencesDelayedPublication(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		records, err := jobs.OpenRepository(store)
		require.NoError(t, err)
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		delayed := &delayedNativePublication{Store: assets, pauseAt: 1, entered: make(chan struct{}), release: make(chan struct{})}
		var release sync.Once
		defer release.Do(func() { close(delayed.release) })
		writer, err := jobs.NewService(records, jobs.WithAssetStore(delayed), jobs.WithRetention(time.Hour), jobs.WithClock(func() time.Time { return submitted }))
		require.NoError(t, err)
		runner := finishingRunner()
		job, err := writer.Submit(t.Context(), open(runner), submissionFor(accountA))
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := writer.Refresh(t.Context(), runner, accountA, job.ID); done <- err }()
		select {
		case <-delayed.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("asset did not reach publication")
		}
		pending, err := records.Get(t.Context(), accountA, job.ID)
		require.NoError(t, err)
		require.Equal(t, delayed.key, pending.AssetKey)
		cleaner, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(func() time.Time { return submitted.Add(2 * time.Hour) }))
		require.NoError(t, err)
		_, err = cleaner.Sweep(t.Context())
		require.NoError(t, err)
		release.Do(func() { close(delayed.release) })
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("asset writer did not finish")
		}
		_, err = assets.StatPublished(t.Context(), delayed.key)
		require.ErrorIs(t, err, blob.ErrNotFound)
		_, _, err = writer.Open(t.Context(), accountA, job.ID)
		require.ErrorIs(t, err, jobs.ErrAssetExpired)
		require.Equal(t, 1, runner.fetches)
	})
}

func TestProviderPreparationPreservesItsBoundAndDeadline(t *testing.T) {
	_, records, assets, clock := newAssetService(t)
	interrupted := &interruptedNativeAssets{Store: assets, failAt: 1}
	original, err := jobs.NewService(records, jobs.WithAssetStore(interrupted), jobs.WithAssetBound(4096), jobs.WithRetention(time.Hour), jobs.WithClock(clock.read))
	require.NoError(t, err)
	runner := finishingRunner()
	job, err := original.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.NoError(t, err)
	job, err = original.Refresh(t.Context(), runner, accountA, job.ID)
	require.NoError(t, err)
	firstKey := job.AssetKey
	require.NotEmpty(t, firstKey)
	require.False(t, job.HasAsset())
	reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithAssetBound(1), jobs.WithRetention(time.Minute), jobs.WithClock(func() time.Time { return submitted.Add(time.Minute) }))
	require.NoError(t, err)
	job, err = reopened.Refresh(t.Context(), runner, accountA, job.ID)
	require.NoError(t, err)
	require.True(t, job.HasAsset())
	require.Equal(t, firstKey, job.AssetKey)
	require.Equal(t, []int64{4096, int64(len(runner.asset.Bytes))}, runner.bounds)
	require.Equal(t, submitted.Add(time.Hour), job.AssetExpiresAt)
	require.Equal(t, 1, runner.submits)
}
