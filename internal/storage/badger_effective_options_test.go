package storage

import (
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
	"github.com/stretchr/testify/require"
)

func TestBadgerEffectiveCompression(t *testing.T) {
	for _, tc := range []struct {
		name        string
		compression options.CompressionType
	}{
		{"none", options.None}, {"snappy", options.Snappy}, {"zstd", options.ZSTD},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := BadgerConfig{Path: t.TempDir(), Compression: tc.name, SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20, GCInterval: time.Minute, GCDiscardRatio: 0.3}
			store, err := OpenBadger(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			check := func() {
				t.Helper()
				actual := store.db.Opts()
				require.Equal(t, tc.compression, actual.Compression)
				require.True(t, actual.SyncWrites)
				require.EqualValues(t, 64<<20, actual.MemTableSize)
			}
			check()
			require.NoError(t, store.Set(t.Context(), "retained", []byte("value")))
			backup := filepath.Join(t.TempDir(), "backup")
			require.NoError(t, store.Backup(t.Context(), backup))
			require.NoError(t, store.Restore(t.Context(), backup))
			check()
			value, err := store.Get(t.Context(), "retained")
			require.NoError(t, err)
			require.Equal(t, "value", string(value))
		})
	}
}

func TestBadgerConfiguredGarbageCollection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var ratios []float64
		store := &BadgerStore{config: BadgerConfig{GCInterval: 10 * time.Second, GCDiscardRatio: 0.3}, gcStop: make(chan struct{})}
		store.startGarbageCollection(func(ratio float64) error {
			mu.Lock()
			ratios = append(ratios, ratio)
			mu.Unlock()
			return badger.ErrNoRewrite
		})
		time.Sleep(25 * time.Second)
		synctest.Wait()
		store.gcTicker.Stop()
		close(store.gcStop)
		store.wg.Wait()
		time.Sleep(25 * time.Second)
		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, []float64{0.3, 0.3}, ratios)
	})
}

func TestBadgerMemoryDoesNotClaimDiskDurability(t *testing.T) {
	store, err := OpenBadger(BadgerConfig{InMemory: true, SyncWrites: true, Compression: "none", NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.True(t, store.db.Opts().InMemory)
	require.False(t, store.db.Opts().SyncWrites)
	require.Nil(t, store.gcTicker)
	require.Nil(t, store.compactTicker)
}
