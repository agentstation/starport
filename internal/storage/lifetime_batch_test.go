package storage

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLifetimeBatch(t *testing.T) {
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
			prefix := "batch-lifetime:" + rand.Text() + ":"
			keys := []string{prefix + "a", prefix + "b", prefix + "empty", prefix + "ttl", prefix + "expired", prefix + "large"}
			t.Cleanup(func() { require.NoError(t, raw.BatchDelete(context.Background(), keys)) })
			require.NoError(t, raw.Set(t.Context(), keys[0], []byte("first")))
			require.NoError(t, raw.Set(t.Context(), keys[1], []byte("second")))
			require.NoError(t, raw.Set(t.Context(), keys[2], []byte{}))
			require.NoError(t, raw.SetWithTTL(t.Context(), keys[3], []byte("temporary"), time.Minute))
			require.NoError(t, raw.Set(t.Context(), keys[4], []byte("expired")))
			require.NoError(t, raw.ExpireAt(t.Context(), keys[4], time.Now().Add(-time.Minute)))
			require.NoError(t, raw.Set(t.Context(), keys[5], []byte(strings.Repeat("x", 65))))
			values, err := authority.ReadBatchWithLifetime(t.Context(), []string{keys[1], prefix + "missing", keys[0], keys[2], keys[3], keys[4]}, 64)
			require.NoError(t, err)
			require.Len(t, values, 6)
			require.Equal(t, "second", string(values[0].Value))
			require.True(t, values[0].Found)
			require.Zero(t, values[0].Lifetime)
			require.False(t, values[1].Found)
			require.Equal(t, "first", string(values[2].Value))
			require.True(t, values[3].Found)
			require.Empty(t, values[3].Value)
			require.True(t, values[4].Found)
			require.Positive(t, values[4].Lifetime)
			require.LessOrEqual(t, values[4].Lifetime, time.Minute)
			require.False(t, values[5].Found)
			values[0].Value[0] = 'X'
			stored, err := raw.Get(t.Context(), keys[1])
			require.NoError(t, err)
			require.Equal(t, "second", string(stored))
			values, err = authority.ReadBatchWithLifetime(t.Context(), []string{keys[0], keys[5]}, 64)
			require.ErrorIs(t, err, ErrValueTooLarge)
			require.Nil(t, values, "an oversized member must discard the entire result")
			for _, invalid := range []struct {
				keys  []string
				bound int
				err   error
			}{
				{nil, 64, ErrInvalidReadLimit},
				{slices.Repeat([]string{keys[0]}, 17), 64, ErrInvalidReadLimit},
				{keys[:1], 0, ErrInvalidReadLimit},
				{keys[:2], maxLifetimeBatchBytes/2 + 1, ErrInvalidReadLimit},
				{[]string{keys[0], keys[0]}, 64, ErrInvalidKey},
				{[]string{""}, 64, ErrInvalidKey},
			} {
				values, err := authority.ReadBatchWithLifetime(t.Context(), invalid.keys, invalid.bound)
				require.ErrorIs(t, err, invalid.err)
				require.Nil(t, values)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			values, err = authority.ReadBatchWithLifetime(ctx, keys[:1], 64)
			require.ErrorIs(t, err, context.Canceled)
			require.Nil(t, values)
			if bound, ok := authority.(*valkeyIncarnationStore); ok {
				stale := *bound
				stale.identity = "unapproved"
				values, err := stale.ReadBatchWithLifetime(t.Context(), keys[:1], 64)
				require.ErrorIs(t, err, ErrIncarnationChanged)
				require.Nil(t, values)
			}

			// Every write changes both records atomically. A batch must never mix revisions.
			require.NoError(t, raw.BatchSet(t.Context(), map[string][]byte{keys[0]: []byte("0"), keys[1]: []byte("0")}))
			started, done := make(chan struct{}), make(chan struct{})
			writeContext, cancelWrites := context.WithCancel(t.Context())
			var writeError error
			go func() {
				defer close(done)
				close(started)
				for i := 1; i <= 100; i++ {
					previous, next := fmt.Appendf(nil, "%d", i-1), fmt.Appendf(nil, "%d", i)
					err := authority.CompareAndSwapInWindow(writeContext, []CompareAndSwapMutation{
						{Key: keys[0], ExpectedValue: previous, NewValue: next},
						{Key: keys[1], ExpectedValue: previous, NewValue: next},
					}, TimeWindow{})
					if err != nil {
						writeError = err
						return
					}
				}
			}()
			t.Cleanup(func() { cancelWrites(); <-done })
			<-started
			for range 100 {
				values, err := authority.ReadBatchWithLifetime(t.Context(), keys[:2], 64)
				require.NoError(t, err)
				require.True(t, values[0].Found && values[1].Found)
				require.Equal(t, values[0].Value, values[1].Value)
			}
			<-done
			require.NoError(t, writeError)
		})
	}
}
