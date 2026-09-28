package jobs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type delayedNativePublication struct {
	blob.Store
	calls            int
	pauseAt          int
	key              string
	entered, release chan struct{}
}

func (b *delayedNativePublication) write(ctx context.Context, key string, r io.Reader, write func(context.Context, string, io.Reader) (blob.Info, error)) (blob.Info, error) {
	b.calls++
	pauseAt := b.pauseAt
	if pauseAt == 0 {
		pauseAt = 2
	}
	if b.calls != pauseAt {
		return write(ctx, key, r)
	}
	b.key = key
	close(b.entered)
	select {
	case <-b.release:
	case <-ctx.Done():
		return blob.Info{}, ctx.Err()
	}
	info, err := write(ctx, key, r)
	if err != nil {
		return info, err
	}
	return info, errors.New("publication acknowledgment lost")
}
func (b *delayedNativePublication) Put(ctx context.Context, key string, r io.Reader) (blob.Info, error) {
	return b.write(ctx, key, r, b.Store.Put)
}
func (b *delayedNativePublication) Publish(ctx context.Context, key string, r io.Reader) (blob.Info, error) {
	return b.write(ctx, key, r, b.Store.Publish)
}

func TestNativeRetirementFencesDelayedPublication(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		assets, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		verifyNativeRetirement(t, store, assets)
	})
}

type repeatedNativeAcceptance struct {
	*nativeRunner
	repeated error
}

func (r *repeatedNativeAcceptance) Submit(ctx context.Context, recorder jobs.SubmissionRecorder) (jobs.Acceptance, error) {
	answer, firstErr := r.nativeRunner.Submit(ctx, recorder)
	if firstErr == nil {
		return answer, nil
	}
	changed := answer
	changed.NativeResult = &jobs.NativeResult{State: jobs.JobStateCompleted, RequestID: "private-native-request", Measurement: &reservation.Evidence{Quantities: reservation.Quantities{"output_seconds": 10}}, Asset: jobs.Asset{ContentType: "video/mp4", Bytes: []byte("changed")}}
	r.repeated = recorder.Accepted(ctx, changed)
	return answer, firstErr
}
func TestNativeReceiptCannotReplaceAcceptedEvidence(t *testing.T) {
	_, records, assets, clock := newAssetService(t)
	interrupted := &interruptedNativeAssets{Store: assets, failAt: 1, after: true}
	service, err := jobs.NewService(records, jobs.WithAssetStore(interrupted), jobs.WithClock(clock.read))
	require.NoError(t, err)
	runner := &repeatedNativeAcceptance{nativeRunner: nativeFixture()}
	job, err := service.Submit(t.Context(), open(runner), submissionFor(accountA))
	require.Error(t, err)
	reopened, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(clock.read))
	require.NoError(t, err)
	recovered, err := reopened.Refresh(t.Context(), runner, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, int64(5), recovered.Measurement.Quantities["output_seconds"], "later acceptance must not replace the first receipt")
	require.Error(t, runner.repeated)
	_, reader, err := reopened.Open(t.Context(), accountA, job.ID)
	require.NoError(t, err)
	defer reader.Close()
	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.True(t, bytes.Equal(runner.asset.Bytes, content))
	require.Equal(t, 1, runner.submits)
}

func verifyNativeRetirement(t *testing.T, store storage.KVStore, assets blob.Store) {
	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	delayed := &delayedNativePublication{Store: assets, entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(delayed.release) })
	writer, err := jobs.NewService(records, jobs.WithAssetStore(delayed), jobs.WithClock(func() time.Time { return submitted }), jobs.WithRetention(time.Hour))
	require.NoError(t, err)
	runner := nativeFixture()
	done := make(chan error, 1)
	go func() { _, err := writer.Submit(t.Context(), open(runner), submissionFor(accountA)); done <- err }()
	select {
	case <-delayed.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("asset did not reach publication")
	}
	owned, err := records.List(t.Context(), accountA, 10)
	require.NoError(t, err)
	require.Len(t, owned, 1)
	cleaner, err := jobs.NewService(records, jobs.WithAssetStore(assets), jobs.WithClock(func() time.Time { return submitted.Add(2 * time.Hour) }))
	require.NoError(t, err)
	expired, err := cleaner.Refresh(t.Context(), runner, accountA, owned[0].ID)
	require.NoError(t, err)
	require.Equal(t, "expired", expired.AssetStatus(submitted))
	release.Do(func() { close(delayed.release) })
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("asset writer did not finish")
	}
	// Both namespaces must lack live content. This assertion also runs against
	// the historical mutable implementation before the repair.
	_, err = assets.Stat(t.Context(), delayed.key)
	require.ErrorIs(t, err, blob.ErrNotFound, "retired video bytes reappeared")
	_, err = assets.StatPublished(t.Context(), delayed.key)
	require.ErrorIs(t, err, blob.ErrNotFound, "retired video bytes reappeared")
	stored, err := records.Get(t.Context(), accountA, owned[0].ID)
	require.NoError(t, err)
	require.Equal(t, runner.measurement.Quantities, stored.Measurement.Quantities)
	require.Equal(t, 1, runner.submits)
}
