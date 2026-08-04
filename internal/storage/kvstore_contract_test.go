package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestKVStoreContract(t *testing.T) {
	t.Run("mock", func(t *testing.T) {
		runKVStoreContract(t, func(t *testing.T) KVStore {
			t.Helper()
			store := NewMockStore()
			t.Cleanup(func() { _ = store.Close() })
			return store
		})
	})

	t.Run("badger", func(t *testing.T) {
		runKVStoreContract(t, func(t *testing.T) KVStore {
			t.Helper()
			store, err := OpenBadger(BadgerConfig{
				Path:         t.TempDir(),
				SyncWrites:   false,
				Compression:  true,
				NumVersions:  1,
				NumLevelZero: 5,
				MemTableSize: 64 << 20,
			})
			if err != nil {
				t.Fatalf("open badger: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return store
		})
	})

	if valkeyURL := os.Getenv("TEST_VALKEY_URL"); valkeyURL != "" {
		t.Run("valkey", func(t *testing.T) {
			runKVStoreContract(t, func(t *testing.T) KVStore {
				t.Helper()
				store, err := OpenValkey(ValkeyConfig{URL: valkeyURL})
				if err != nil {
					t.Fatalf("open valkey: %v", err)
				}
				t.Cleanup(func() { _ = store.Close() })
				return store
			})
		})
	}
}

func runKVStoreContract(t *testing.T, openStore func(*testing.T) KVStore) {
	t.Helper()

	t.Run("basic operations", func(t *testing.T) {
		store := openStore(t)
		ctx := context.Background()
		key := contractKey(t, "basic")

		if err := store.Set(ctx, key, []byte("value")); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if string(got) != "value" {
			t.Fatalf("got %q, want value", got)
		}
		exists, err := store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("exists: %v", err)
		}
		if !exists {
			t.Fatal("key should exist")
		}
		if err := store.Delete(ctx, key); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get deleted key: got %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("ttl operations", func(t *testing.T) {
		store := openStore(t)
		ctx := context.Background()
		key := contractKey(t, "ttl")

		if err := store.SetWithTTL(ctx, key, []byte("ttl-value"), 5*time.Second); err != nil {
			t.Fatalf("set with ttl: %v", err)
		}
		ttl, err := store.GetTTL(ctx, key)
		if err != nil {
			t.Fatalf("get ttl: %v", err)
		}
		if ttl <= 0 {
			t.Fatalf("ttl = %v, want positive duration", ttl)
		}
		if err := store.ExpireAt(ctx, key, time.Now().Add(-time.Second)); err != nil {
			t.Fatalf("expire at past: %v", err)
		}
		if _, err := store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get expired key: got %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("atomic operations", func(t *testing.T) {
		store := openStore(t)
		ctx := context.Background()
		key := contractKey(t, "atomic")

		got, err := store.Increment(ctx, key, 1)
		if err != nil {
			t.Fatalf("increment missing key: %v", err)
		}
		if got != 1 {
			t.Fatalf("increment = %d, want 1", got)
		}
		got, err = store.Increment(ctx, key, 3)
		if err != nil {
			t.Fatalf("increment existing key: %v", err)
		}
		if got != 4 {
			t.Fatalf("increment = %d, want 4", got)
		}
		if err := store.CompareAndSwap(ctx, key, SerializeInt64(4), SerializeInt64(9)); err != nil {
			t.Fatalf("compare and swap: %v", err)
		}
		data, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("get cas key: %v", err)
		}
		value, err := DeserializeInt64(data)
		if err != nil {
			t.Fatalf("deserialize cas value: %v", err)
		}
		if value != 9 {
			t.Fatalf("cas value = %d, want 9", value)
		}
		if err := store.CompareAndSwap(ctx, key, SerializeInt64(4), SerializeInt64(10)); !errors.Is(err, ErrConflict) {
			t.Fatalf("compare and swap stale value: got %v, want %v", err, ErrConflict)
		}

		createKey := contractKey(t, "cas-create-delete")
		if err := store.CompareAndSwap(ctx, createKey, nil, []byte("created")); err != nil {
			t.Fatalf("compare and swap create: %v", err)
		}
		if err := store.CompareAndSwap(ctx, createKey, nil, []byte("duplicate")); !errors.Is(err, ErrConflict) {
			t.Fatalf("compare and swap duplicate create: got %v, want %v", err, ErrConflict)
		}
		if err := store.CompareAndSwap(ctx, createKey, []byte("created"), nil); err != nil {
			t.Fatalf("compare and swap delete: %v", err)
		}
		if _, err := store.Get(ctx, createKey); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get compare and swap deleted key: got %v, want %v", err, ErrNotFound)
		}

		batchKeyA := contractKey(t, "cas-batch-a")
		batchKeyB := contractKey(t, "cas-batch-b")
		if err := store.CompareAndSwapBatch(ctx, []CompareAndSwapMutation{
			{Key: batchKeyA, NewValue: []byte("a")},
			{Key: batchKeyB, NewValue: []byte("b"), TTL: time.Minute},
		}); err != nil {
			t.Fatalf("compare and swap batch create: %v", err)
		}
		ttl, err := store.GetTTL(ctx, batchKeyB)
		if err != nil || ttl <= 0 {
			t.Fatalf("compare and swap batch TTL = %v, %v; want positive, nil", ttl, err)
		}
		if err := store.CompareAndSwapBatch(ctx, []CompareAndSwapMutation{
			{Key: batchKeyA, ExpectedValue: []byte("stale"), NewValue: []byte("changed-a")},
			{Key: batchKeyB, ExpectedValue: []byte("b"), NewValue: []byte("changed-b")},
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("compare and swap batch conflict: got %v, want %v", err, ErrConflict)
		}
		data, err = store.Get(ctx, batchKeyB)
		if err != nil || string(data) != "b" {
			t.Fatalf("batch conflict changed second key to %q, %v", data, err)
		}
	})

	t.Run("batch operations", func(t *testing.T) {
		store := openStore(t)
		ctx := context.Background()
		keyA := contractKey(t, "batch-a")
		keyB := contractKey(t, "batch-b")

		if err := store.BatchSet(ctx, map[string][]byte{
			keyA: []byte("a"),
			keyB: []byte("b"),
		}); err != nil {
			t.Fatalf("batch set: %v", err)
		}
		got, err := store.BatchGet(ctx, []string{keyA, keyB})
		if err != nil {
			t.Fatalf("batch get: %v", err)
		}
		if string(got[keyA]) != "a" || string(got[keyB]) != "b" {
			t.Fatalf("batch get = %#v, want a and b", got)
		}
		if err := store.BatchDelete(ctx, []string{keyA, keyB}); err != nil {
			t.Fatalf("batch delete: %v", err)
		}
		for _, key := range []string{keyA, keyB} {
			if exists, err := store.Exists(ctx, key); err != nil || exists {
				t.Fatalf("exists(%s) = %v, %v; want false, nil", key, exists, err)
			}
		}
	})

	t.Run("transactions", func(t *testing.T) {
		store := openStore(t)
		ctx := context.Background()
		committedKey := contractKey(t, "tx-commit")
		rolledBackKey := contractKey(t, "tx-rollback")

		tx, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("begin commit transaction: %v", err)
		}
		if err := tx.Set(committedKey, []byte("committed")); err != nil {
			t.Fatalf("transaction set: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("transaction commit: %v", err)
		}
		data, err := store.Get(ctx, committedKey)
		if err != nil {
			t.Fatalf("get committed key: %v", err)
		}
		if string(data) != "committed" {
			t.Fatalf("committed value = %q, want committed", data)
		}

		tx, err = store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("begin rollback transaction: %v", err)
		}
		if err := tx.Set(rolledBackKey, []byte("rolled-back")); err != nil {
			t.Fatalf("transaction set rollback key: %v", err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatalf("transaction rollback: %v", err)
		}
		if _, err := store.Get(ctx, rolledBackKey); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get rolled back key: got %v, want %v", err, ErrNotFound)
		}
	})
}

func contractKey(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("contract:%s:%d:%s", t.Name(), time.Now().UnixNano(), suffix)
}
