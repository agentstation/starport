// Package repotest supplies storage backends for repository contract tests.
package repotest

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/google/uuid"
)

// Run runs one repository contract against each configured backend.
func Run(t *testing.T, contract func(*testing.T, storage.KVStore)) {
	t.Helper()
	runBackends(t, contract, func(t *testing.T, run func(*testing.T)) { run(t) })
}

// RunWithClock separates lifecycle assertions from real CPU scheduling time.
// Backend resources start and stop inside each synctest clock.
func RunWithClock(t *testing.T, contract func(*testing.T, storage.KVStore)) {
	t.Helper()
	runBackends(t, contract, synctest.Test)
}

func runBackends(t *testing.T, contract func(*testing.T, storage.KVStore), clock func(*testing.T, func(*testing.T))) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		clock(t, func(t *testing.T) {
			store := storage.NewMockStore()
			t.Cleanup(func() { _ = store.Close() })
			contract(t, newNamespacedStore(t, store))
		})
	})
	t.Run("badger", func(t *testing.T) {
		clock(t, func(t *testing.T) {
			store, err := storage.OpenBadger(storage.BadgerConfig{
				Path:         t.TempDir(),
				SyncWrites:   true,
				Compression:  "snappy",
				NumVersions:  1,
				NumLevelZero: 5,
				MemTableSize: 64 << 20,
			})
			if err != nil {
				t.Fatalf("open Badger: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			contract(t, newNamespacedStore(t, store))
		})
	})
	t.Run("valkey", func(t *testing.T) {
		clock(t, func(t *testing.T) {
			url := os.Getenv("TEST_VALKEY_URL")
			if url == "" {
				t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
			}
			store, err := storage.OpenValkey(storage.ValkeyConfig{DeploymentID: "contract-tests", URL: url})
			if err != nil {
				t.Fatalf("open Valkey: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			contract(t, newNamespacedStore(t, store))
		})
	})
}

type namespacedStore struct {
	storage.KVStore
	prefix string
}

// NamespacedStore lets separate test processes share one isolated key namespace.
// The caller owns namespace cleanup and the underlying store.
func NamespacedStore(store storage.KVStore, prefix string) storage.KVStore {
	return &namespacedStore{KVStore: store, prefix: prefix}
}

func newNamespacedStore(t *testing.T, store storage.KVStore) *namespacedStore {
	t.Helper()
	scoped := &namespacedStore{KVStore: store, prefix: "repotest:" + uuid.NewString() + ":"}
	t.Cleanup(func() {
		keys, err := store.ScanWithPrefix(context.Background(), scoped.prefix, 0)
		if err == nil && len(keys) > 0 {
			err = store.BatchDelete(context.Background(), keys)
		}
		if err != nil {
			t.Errorf("clean repository test namespace: %v", err)
		}
	})
	return scoped
}

func (s *namespacedStore) key(key string) string { return s.prefix + key }

func (s *namespacedStore) logicalKey(key string) string {
	return strings.TrimPrefix(key, s.prefix)
}

func (s *namespacedStore) GetBounded(ctx context.Context, key string, limit int) ([]byte, error) {
	return s.KVStore.GetBounded(ctx, s.key(key), limit)
}

func (s *namespacedStore) Get(ctx context.Context, key string) ([]byte, error) {
	return s.KVStore.Get(ctx, s.key(key))
}

func (s *namespacedStore) Set(ctx context.Context, key string, value []byte) error {
	return s.KVStore.Set(ctx, s.key(key), value)
}

func (s *namespacedStore) Delete(ctx context.Context, key string) error {
	return s.KVStore.Delete(ctx, s.key(key))
}

func (s *namespacedStore) Exists(ctx context.Context, key string) (bool, error) {
	return s.KVStore.Exists(ctx, s.key(key))
}

func (s *namespacedStore) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.KVStore.SetWithTTL(ctx, s.key(key), value, ttl)
}

func (s *namespacedStore) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	return s.KVStore.GetTTL(ctx, s.key(key))
}

func (s *namespacedStore) ExpireAt(ctx context.Context, key string, expireAt time.Time) error {
	return s.KVStore.ExpireAt(ctx, s.key(key), expireAt)
}

func (s *namespacedStore) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	return s.KVStore.Increment(ctx, s.key(key), delta)
}

func (s *namespacedStore) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	return s.KVStore.Decrement(ctx, s.key(key), delta)
}

func (s *namespacedStore) CompareAndSwap(ctx context.Context, key string, old, newValue []byte) error {
	return s.KVStore.CompareAndSwap(ctx, s.key(key), old, newValue)
}

func (s *namespacedStore) CompareAndSwapBatch(ctx context.Context, mutations []storage.CompareAndSwapMutation) error {
	scoped := make([]storage.CompareAndSwapMutation, len(mutations))
	for index, mutation := range mutations {
		scoped[index] = mutation
		scoped[index].Key = s.key(mutation.Key)
	}
	return s.KVStore.CompareAndSwapBatch(ctx, scoped)
}

func (s *namespacedStore) BatchGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	scopedKeys := make([]string, len(keys))
	for index, key := range keys {
		scopedKeys[index] = s.key(key)
	}
	values, err := s.KVStore.BatchGet(ctx, scopedKeys)
	if err != nil {
		return nil, err
	}
	logical := make(map[string][]byte, len(values))
	for key, value := range values {
		logical[s.logicalKey(key)] = value
	}
	return logical, nil
}

func (s *namespacedStore) BatchSet(ctx context.Context, items map[string][]byte) error {
	return s.KVStore.BatchSet(ctx, s.scopedItems(items))
}

func (s *namespacedStore) BatchDelete(ctx context.Context, keys []string) error {
	scoped := make([]string, len(keys))
	for index, key := range keys {
		scoped[index] = s.key(key)
	}
	return s.KVStore.BatchDelete(ctx, scoped)
}

func (s *namespacedStore) BatchSetWithTTL(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	return s.KVStore.BatchSetWithTTL(ctx, s.scopedItems(items), ttl)
}

func (s *namespacedStore) scopedItems(items map[string][]byte) map[string][]byte {
	scoped := make(map[string][]byte, len(items))
	for key, value := range items {
		scoped[s.key(key)] = value
	}
	return scoped
}

func (s *namespacedStore) Scan(ctx context.Context, pattern string, limit int) ([]string, error) {
	return s.scan(ctx, s.prefix+pattern, limit)
}

func (s *namespacedStore) ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error) {
	return s.scanPrefix(ctx, s.key(prefix), limit)
}

func (s *namespacedStore) scan(ctx context.Context, pattern string, limit int) ([]string, error) {
	keys, err := s.KVStore.Scan(ctx, pattern, limit)
	if err != nil {
		return nil, err
	}
	return s.logicalKeys(keys), nil
}

func (s *namespacedStore) scanPrefix(ctx context.Context, prefix string, limit int) ([]string, error) {
	keys, err := s.KVStore.ScanWithPrefix(ctx, prefix, limit)
	if err != nil {
		return nil, err
	}
	return s.logicalKeys(keys), nil
}

func (s *namespacedStore) logicalKeys(keys []string) []string {
	logical := make([]string, len(keys))
	for index, key := range keys {
		logical[index] = s.logicalKey(key)
	}
	return logical
}

func (s *namespacedStore) ScanPage(ctx context.Context, prefix, cursor string, count int) (storage.KeyPage, error) {
	page, err := s.KVStore.ScanPage(ctx, s.key(prefix), cursor, count)
	if err != nil {
		return storage.KeyPage{}, err
	}
	for i, key := range page.Keys {
		page.Keys[i] = s.logicalKey(key)
	}
	return page, nil
}
