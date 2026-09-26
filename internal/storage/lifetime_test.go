package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValueLifetimeSnapshot(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			var store KVStore
			var err error
			if backend == "badger" {
				store, err = OpenBadger(BadgerConfig{Path: t.TempDir(), NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20})
			} else {
				address := os.Getenv("TEST_VALKEY_URL")
				if address == "" {
					t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
				}
				store, err = OpenValkey(ValkeyConfig{URL: address})
			}
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			reader := store.(LifetimeReader)
			key := "lifetime:" + t.Name()
			t.Cleanup(func() { _ = store.Delete(context.Background(), key) })
			ctx := t.Context()
			require.NoError(t, store.SetWithTTL(ctx, key, []byte("short"), time.Minute))
			value, ttl, err := reader.ReadWithLifetime(ctx, key, 64)
			require.NoError(t, err)
			require.Equal(t, "short", string(value))
			require.Positive(t, ttl)
			require.LessOrEqual(t, ttl, time.Minute)
			value, ttl, err = reader.ReadWithLifetime(ctx, key, 1)
			require.ErrorIs(t, err, ErrValueTooLarge)
			require.Nil(t, value)
			require.Zero(t, ttl)
			require.NoError(t, store.Set(ctx, key, []byte("persistent")))
			_, ttl, err = reader.ReadWithLifetime(ctx, key, 64)
			require.NoError(t, err)
			require.Zero(t, ttl)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, _, err = reader.ReadWithLifetime(canceled, key, 64)
			require.ErrorIs(t, err, context.Canceled)
			require.NoError(t, store.SetWithTTL(ctx, key, []byte("short"), time.Minute))
			done := make(chan struct{})
			var writerErr error
			t.Cleanup(func() { <-done; require.NoError(t, writerErr) })
			go func() {
				defer close(done)
				for i := range 128 {
					data, lifetime := "short", time.Minute
					if i%2 == 0 {
						data, lifetime = "long", 10*time.Minute
					}
					if err := store.SetWithTTL(ctx, key, []byte(data), lifetime); err != nil {
						writerErr = err
						return
					}
				}
			}()
			for range 128 {
				value, ttl, err = reader.ReadWithLifetime(ctx, key, 64)
				require.NoError(t, err)
				if string(value) == "short" {
					require.LessOrEqual(t, ttl, time.Minute)
				} else {
					require.Equal(t, "long", string(value))
					require.Greater(t, ttl, 2*time.Minute)
				}
			}
			<-done
			require.NoError(t, writerErr)
			require.NoError(t, store.Delete(ctx, key))
			_, _, err = reader.ReadWithLifetime(ctx, key, 64)
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}
