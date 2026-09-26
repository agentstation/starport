package cache

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type heldFillCache struct {
	ResponseStore
	release chan struct{}
}

func (s *heldFillCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return s.ResponseStore.Set(ctx, key, value, ttl)
	}
}

func TestResponseFillDoesNotWaitForStorage(t *testing.T) {
	local, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	store := &heldFillCache{ResponseStore: local, release: make(chan struct{})}
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, store)
	require.NoError(t, err)
	returned := make(chan struct{})
	var fillErr error
	go func() {
		defer close(returned)
		fillErr = manager.SetResponse(t.Context(), "key", []byte("answer"))
	}()
	defer func() {
		close(store.release)
		<-returned
		require.NoError(t, manager.Close())
	}()
	select {
	case <-returned:
		require.NoError(t, fillErr)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("optional cache storage blocked the caller")
	}
}

func TestResponseFillQueueBoundsAndShutdown(t *testing.T) {
	local, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	store := &heldFillCache{ResponseStore: local, release: make(chan struct{})}
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	value := make([]byte, 1<<20)
	for i := range 10 {
		require.NoError(t, manager.SetResponse(t.Context(), strconv.Itoa(i), value))
	}
	stats := manager.Stats()["response_fills"]
	require.LessOrEqual(t, stats.RetainedBytes, int64(fillQueueBytes))
	require.LessOrEqual(t, stats.RetainedEntries, int64(fillQueueEntries))
	require.LessOrEqual(t, stats.ActiveFills, int64(fillWorkers))
	require.Positive(t, stats.DroppedFills)
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("cache shutdown did not cancel active fills")
	}
	stats = manager.Stats()["response_fills"]
	require.Zero(t, stats.RetainedBytes)
	require.Zero(t, stats.RetainedEntries)
	require.Zero(t, stats.ActiveFills)
	require.ErrorIs(t, manager.SetResponse(t.Context(), "closed", nil), ErrCacheClosed)
}

func TestResponseFillOwnsBytes(t *testing.T) {
	local, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	store := &heldFillCache{ResponseStore: local, release: make(chan struct{})}
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	value := []byte("answer")
	require.NoError(t, manager.SetResponse(t.Context(), "key", value))
	value[0] = 'X'
	close(store.release)
	awaitResponse(t, manager, "key", []byte("answer"))
}

type slowReadCache struct{ ResponseStore }

func (s slowReadCache) Get(ctx context.Context, _ string) ([]byte, bool, error) {
	<-ctx.Done()
	return nil, false, ctx.Err()
}

func TestResponseCacheReadUsesDeadline(t *testing.T) {
	local, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, slowReadCache{local})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	value, found, err := manager.GetResponse(ctx, "key")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, ctx.Err())
	require.False(t, found)
	require.Nil(t, value)
}

func TestExpiredQueuedFillDoesNotAcquireNewLifetime(t *testing.T) {
	local, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	store := &heldFillCache{ResponseStore: local, release: make(chan struct{})}
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	for i := range fillWorkers {
		require.NoError(t, manager.SetResponse(t.Context(), strconv.Itoa(i), []byte("answer")))
		require.Eventually(t, func() bool { return manager.fills.stats().ActiveFills == int64(i+1) }, time.Second, time.Millisecond)
	}
	require.Eventually(t, func() bool { return manager.fills.stats().ActiveFills == fillWorkers }, time.Second, time.Millisecond)
	require.NoError(t, manager.fills.enqueue(t.Context(), "expired", []byte("stale"), time.Millisecond))
	require.Equal(t, int64(fillWorkers+1), manager.fills.stats().RetainedEntries)
	time.Sleep(3 * time.Millisecond)
	close(store.release)
	require.Eventually(t, func() bool { return manager.fills.stats().RetainedEntries == 0 }, time.Second, time.Millisecond)
	_, found, err := local.Get(t.Context(), "expired")
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, uint64(1), manager.fills.stats().DroppedFills)
}

type failedFillCache struct{ ResponseStore }

func (s failedFillCache) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("cache unavailable")
}

func TestResponseFillFailureReportsStatus(t *testing.T) {
	local, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, failedFillCache{local})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	require.NoError(t, manager.SetResponse(t.Context(), "key", []byte("answer")))
	require.Eventually(t, func() bool { return manager.FillStatus().FailedFills == 1 }, time.Second, time.Millisecond)
	require.Zero(t, manager.FillStatus().CompletedFills)
}
