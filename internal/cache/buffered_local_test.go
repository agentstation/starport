package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBufferedLocalFillBoundsOwnershipAndShutdown(t *testing.T) {
	store, err := NewBufferedLocalCache(1, time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	// Hold the actual writer to observe admitted work without timing a cache barrier.
	held := &heldFillCache{ResponseStore: store.local, release: make(chan struct{})}
	store.fills.store = held
	value := []byte("original")
	require.NoError(t, store.Set(t.Context(), "entry", value, time.Minute))
	value[0] = 'X'
	for range 10 {
		require.NoError(t, store.Set(t.Context(), "large", make([]byte, 1<<20), time.Minute))
	}
	status := store.FillStatus()
	require.LessOrEqual(t, status.RetainedBytes, status.ByteLimit)
	require.LessOrEqual(t, status.RetainedEntries, status.EntryLimit)
	require.Positive(t, status.DroppedFills)
	close(held.release)
	require.Eventually(t, func() bool {
		value, found, err := store.Get(t.Context(), "entry")
		return err == nil && found && string(value) == "original"
	}, time.Second, time.Millisecond)
	require.NoError(t, store.Close())
	require.Zero(t, store.FillStatus().RetainedEntries)
	require.Zero(t, store.FillStatus().RetainedBytes)
	require.ErrorIs(t, store.Set(t.Context(), "closed", nil, time.Minute), ErrCacheClosed)
}

func TestBufferedLocalCancelsBlockedFillOnClose(t *testing.T) {
	store, err := NewBufferedLocalCache(1, time.Minute)
	require.NoError(t, err)
	held := &heldFillCache{ResponseStore: store.local, release: make(chan struct{})}
	store.fills.store = held
	require.NoError(t, store.Set(t.Context(), "entry", []byte("answer"), time.Minute))
	require.Eventually(t, func() bool { return store.FillStatus().ActiveFills == 1 }, time.Second, time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("close did not cancel active local fill")
	}
	require.Zero(t, store.FillStatus().RetainedBytes)
}
