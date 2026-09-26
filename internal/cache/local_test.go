package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLocalCacheExpiryAndClear(t *testing.T) {
	store, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.NoError(t, store.Set(t.Context(), "entry", []byte("answer"), 50*time.Millisecond))
	value, found, err := store.Get(t.Context(), "entry")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "answer", string(value))
	require.Eventually(t, func() bool { _, found, err := store.Get(t.Context(), "entry"); return err == nil && !found }, time.Second, time.Millisecond)
	require.NoError(t, store.Set(t.Context(), "entry", []byte("answer"), 0))
	store.Clear()
	_, found, err = store.Get(t.Context(), "entry")
	require.NoError(t, err)
	require.False(t, found)
}

func TestLocalCacheChargesRetainedBuffer(t *testing.T) {
	store, err := NewLocalCache(1, time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	// A short slice still retains its full backing allocation.
	value := make([]byte, 1, 2<<20)
	require.NoError(t, store.Set(t.Context(), "oversized", value, time.Minute))
	_, found, err := store.Get(t.Context(), "oversized")
	require.NoError(t, err)
	require.False(t, found, "a retained allocation larger than the cache must not be admitted")
}
