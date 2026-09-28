package storage

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimeBoundStore(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			var raw KVStore
			var authority TimeBoundStore
			if backend == "badger" {
				store, err := OpenBadger(BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
				require.NoError(t, err)
				raw, authority = store, store
				t.Cleanup(func() { require.NoError(t, store.Close()) })
			} else {
				store := incarnationTestStore(t, "TEST_VALKEY_URL")
				id, err := store.ObserveIncarnation(t.Context())
				require.NoError(t, err)
				bound, err := store.BindIncarnation(t.Context(), id)
				require.NoError(t, err)
				raw, authority = store, bound.(TimeBoundStore)
			}
			now, err := authority.AuthorityTime(t.Context())
			require.NoError(t, err)
			require.WithinDuration(t, time.Now(), now, time.Minute)
			start := now.Truncate(time.Second)
			key := "time-window:" + rand.Text()
			t.Cleanup(func() { require.NoError(t, raw.Delete(context.Background(), key)) })
			mutations := []CompareAndSwapMutation{{Key: key, NewValue: []byte("reserved")}}
			for _, w := range []TimeWindow{
				{Start: start.Add(-time.Hour), End: start},
				{Start: start.Add(time.Hour), End: start.Add(2 * time.Hour)},
			} {
				require.ErrorIs(t, authority.CompareAndSwapInWindow(t.Context(), mutations, w), ErrTimeWindowChanged)
				_, err := raw.Get(t.Context(), key)
				require.ErrorIs(t, err, ErrNotFound)
			}
			window := TimeWindow{Start: start.Add(-time.Minute), End: start.Add(time.Hour)}
			require.NoError(t, authority.CompareAndSwapInWindow(t.Context(), mutations, window))
			require.ErrorIs(t, authority.CompareAndSwapInWindow(t.Context(), mutations, window), ErrConflict)
			mutations[0].ExpectedValue, mutations[0].NewValue = []byte("reserved"), []byte("settled")
			require.NoError(t, authority.CompareAndSwapInWindow(t.Context(), mutations, TimeWindow{}))
			value, ttl, err := authority.ReadWithLifetime(t.Context(), key, 64)
			require.NoError(t, err)
			require.Equal(t, "settled", string(value))
			require.Zero(t, ttl)
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = authority.AuthorityTime(canceled)
			require.ErrorIs(t, err, context.Canceled)
			require.ErrorIs(t, authority.CompareAndSwapInWindow(canceled, mutations, window), context.Canceled)
			require.ErrorIs(t, authority.CompareAndSwapInWindow(t.Context(), mutations, TimeWindow{Start: start}), ErrInvalidMutation)
			if backend == "valkey" {
				bound := authority.(*valkeyIncarnationStore)
				stale := *bound
				stale.identity = "unapproved"
				_, err := stale.AuthorityTime(t.Context())
				require.ErrorIs(t, err, ErrIncarnationChanged)
				require.ErrorIs(t, stale.CompareAndSwapInWindow(t.Context(), mutations, window), ErrIncarnationChanged)
			}
		})
	}
}
