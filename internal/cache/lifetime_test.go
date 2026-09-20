package cache

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func lifetimeTestStore(t *testing.T) *storage.BadgerStore {
	t.Helper()
	store, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func TestRefillPreservesBackingExpiry(t *testing.T) {
	for _, kind := range []string{"layered", "hybrid"} {
		for _, operation := range []string{"get", "multi", "warm"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				store := lifetimeTestStore(t)
				var subject Cache
				var settle func()
				if kind == "hybrid" {
					hybrid, err := NewHybridCache(HybridCacheConfig{LocalSizeMB: 1, LocalTTL: time.Minute}, store, nil)
					require.NoError(t, err)
					subject, settle = hybrid, hybrid.local.Wait
				} else {
					layered, err := New(Config{MaxSize: 100, MaxSizeInMB: 1, DefaultTTL: time.Minute}, store)
					require.NoError(t, err)
					subject, settle = layered, layered.(*layeredCache).local.Wait
				}
				t.Cleanup(func() { require.NoError(t, subject.Close()) })
				ctx := t.Context()
				require.NoError(t, store.SetWithTTL(ctx, "entry", []byte("answer"), 2*time.Second))
				switch operation {
				case "get":
					value, found, err := subject.Get(ctx, "entry")
					require.NoError(t, err)
					require.True(t, found)
					require.Equal(t, "answer", string(value))
				case "multi":
					values, err := subject.GetMulti(ctx, []string{"entry"})
					require.NoError(t, err)
					require.Equal(t, "answer", string(values["entry"]))
				case "warm":
					require.NoError(t, subject.Warm(ctx, []string{"entry"}))
				}
				settle()
				require.Eventually(t, func() bool {
					_, err := store.Get(ctx, "entry")
					return errors.Is(err, storage.ErrNotFound)
				}, 4*time.Second, 10*time.Millisecond)
				value, found, err := subject.Get(ctx, "entry")
				require.NoError(t, err)
				require.False(t, found, "local refill extended the expired backing value: %q", value)
			})
		}
	}
}

func TestUnknownLifetimeDoesNotRefill(t *testing.T) {
	for _, kind := range []string{"layered", "hybrid"} {
		t.Run(kind, func(t *testing.T) {
			store := storage.NewMockStore()
			subject := lifetimeTestCache(t, kind, store)
			require.NoError(t, store.Set(t.Context(), "key", []byte("old")))
			_, found, err := subject.Get(t.Context(), "key")
			require.NoError(t, err)
			require.True(t, found)
			require.NoError(t, store.Set(t.Context(), "key", []byte("new")))
			value, found, err := subject.Get(t.Context(), "key")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "new", string(value))
		})
	}
}

func lifetimeTestCache(t *testing.T, kind string, store storage.KVStore) Cache {
	t.Helper()
	var subject Cache
	var err error
	if kind == "hybrid" {
		subject, err = NewHybridCache(HybridCacheConfig{LocalSizeMB: 1, LocalTTL: time.Minute}, store, nil)
	} else {
		subject, err = New(Config{MaxSize: 100, MaxSizeInMB: 1, DefaultTTL: time.Minute}, store)
	}
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, subject.Close()) })
	return subject
}

type delayedLifetimeStore struct{ storage.KVStore }

func (s delayedLifetimeStore) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	value, err := s.GetBounded(ctx, key, maxBytes)
	time.Sleep(30 * time.Millisecond)
	return value, time.Millisecond, err
}

func TestTransferDelayConsumesLifetime(t *testing.T) {
	for _, kind := range []string{"layered", "hybrid"} {
		t.Run(kind, func(t *testing.T) {
			store := delayedLifetimeStore{storage.NewMockStore()}
			require.NoError(t, store.Set(t.Context(), "key", []byte("answer")))
			subject := lifetimeTestCache(t, kind, store)
			value, found, err := subject.Get(t.Context(), "key")
			require.NoError(t, err)
			require.False(t, found)
			require.Nil(t, value)
		})
	}
}

func TestValkeyRefillPreservesExpiry(t *testing.T) {
	address := os.Getenv("TEST_VALKEY_URL")
	if address == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}
	store, err := storage.OpenValkey(storage.ValkeyConfig{URL: address})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	subject := lifetimeTestCache(t, "hybrid", store)
	key := "cache-lifetime:" + t.Name()
	t.Cleanup(func() { _ = store.Delete(context.Background(), key) })
	require.NoError(t, store.SetWithTTL(t.Context(), key, []byte("answer"), time.Second))
	value, found, err := subject.Get(t.Context(), key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "answer", string(value))
	require.Eventually(t, func() bool {
		_, err := store.Get(t.Context(), key)
		return errors.Is(err, storage.ErrNotFound)
	}, 3*time.Second, 10*time.Millisecond)
	value, found, err = subject.Get(t.Context(), key)
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, value)
}

func TestRetainedEntryCannotOutliveSourceDeadline(t *testing.T) {
	for _, kind := range []string{"layered", "hybrid"} {
		t.Run(kind, func(t *testing.T) {
			subject := lifetimeTestCache(t, kind, storage.NewMockStore())
			entry := expiringValue{data: []byte("expired"), deadline: time.Now().Add(-time.Hour)}
			switch c := subject.(type) {
			case *layeredCache:
				c.local.SetWithTTL("key", entry, 64, time.Hour)
				c.local.Wait()
			case *HybridCache:
				c.local.SetWithTTL("key", entry, 64, time.Hour)
				c.local.Wait()
			}
			found, err := subject.Exists(t.Context(), "key")
			require.NoError(t, err)
			require.False(t, found)
			value, found, err := subject.Get(t.Context(), "key")
			require.NoError(t, err)
			require.False(t, found)
			require.Nil(t, value)
		})
	}
}
