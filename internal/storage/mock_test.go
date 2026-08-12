package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockStore_BasicOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	defer store.Close()

	t.Run("Set and Get", func(t *testing.T) {
		key := "test-key"
		value := []byte("test-value")

		// Set value
		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Get value
		got, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if string(got) != string(value) {
			t.Errorf("Get returned %s, want %s", got, value)
		}
	})

	t.Run("Get non-existent key", func(t *testing.T) {
		_, err := store.Get(ctx, "non-existent")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound, got %v", err)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		key := "delete-test"
		value := []byte("value")

		// Set value
		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Delete
		err = store.Delete(ctx, key)
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		// Verify deleted
		_, err = store.Get(ctx, key)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("Exists", func(t *testing.T) {
		key := "exists-test"
		value := []byte("value")

		// Check non-existent
		exists, err := store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if exists {
			t.Error("Expected false for non-existent key")
		}

		// Set value
		err = store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Check existent
		exists, err = store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Error("Expected true for existing key")
		}
	})
}

func TestMockStore_TTLOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	defer store.Close()

	t.Run("SetWithTTL", func(t *testing.T) {
		key := "ttl-test"
		value := []byte("value")
		ttl := 100 * time.Millisecond

		// Set with TTL
		err := store.SetWithTTL(ctx, key, value, ttl)
		if err != nil {
			t.Fatalf("SetWithTTL failed: %v", err)
		}

		// Value should exist
		got, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if string(got) != string(value) {
			t.Errorf("Get returned %s, want %s", got, value)
		}

		// Wait for expiration
		waitForExpiration(t, store, key, ttl+100*time.Millisecond)

		// Value should be gone
		_, err = store.Get(ctx, key)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound after TTL, got %v", err)
		}
	})

	t.Run("GetTTL", func(t *testing.T) {
		key := "ttl-test-2"
		value := []byte("value")
		ttl := 1 * time.Second

		// Set with TTL
		err := store.SetWithTTL(ctx, key, value, ttl)
		if err != nil {
			t.Fatalf("SetWithTTL failed: %v", err)
		}

		// Get TTL
		remaining, err := store.GetTTL(ctx, key)
		if err != nil {
			t.Fatalf("GetTTL failed: %v", err)
		}

		// Should be close to original TTL
		if remaining > ttl || remaining < ttl-100*time.Millisecond {
			t.Errorf("GetTTL returned %v, expected close to %v", remaining, ttl)
		}

		// Test key without TTL
		keyNoTTL := "no-ttl"
		err = store.Set(ctx, keyNoTTL, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		remaining, err = store.GetTTL(ctx, keyNoTTL)
		if err != nil {
			t.Fatalf("GetTTL failed: %v", err)
		}
		if remaining != -1 {
			t.Errorf("GetTTL for key without TTL should return -1, got %v", remaining)
		}
	})

	t.Run("ExpireAt", func(t *testing.T) {
		key := "expire-at-test"
		value := []byte("value")

		// Set value
		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Set expiration
		expireAt := time.Now().Add(100 * time.Millisecond)
		err = store.ExpireAt(ctx, key, expireAt)
		if err != nil {
			t.Fatalf("ExpireAt failed: %v", err)
		}

		// Value should exist
		exists, err := store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Error("Key should exist before expiration")
		}

		// Wait for expiration
		waitForKeyNotExists(t, store, key, 200*time.Millisecond)

		// Value should be gone
		exists, err = store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if exists {
			t.Error("Key should not exist after expiration")
		}
	})
}

func TestMockStore_AtomicOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	defer store.Close()

	t.Run("Increment", func(t *testing.T) {
		key := "counter"

		// Increment non-existent key
		val, err := store.Increment(ctx, key, 5)
		if err != nil {
			t.Fatalf("Increment failed: %v", err)
		}
		if val != 5 {
			t.Errorf("Expected 5, got %d", val)
		}

		// Increment existing key
		val, err = store.Increment(ctx, key, 3)
		if err != nil {
			t.Fatalf("Increment failed: %v", err)
		}
		if val != 8 {
			t.Errorf("Expected 8, got %d", val)
		}

		// Verify stored value
		data, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		stored, err := DeserializeInt64(data)
		if err != nil {
			t.Fatalf("DeserializeInt64 failed: %v", err)
		}
		if stored != 8 {
			t.Errorf("Stored value should be 8, got %d", stored)
		}
	})

	t.Run("Decrement", func(t *testing.T) {
		key := "counter-dec"

		// Set initial value
		err := store.Set(ctx, key, SerializeInt64(10))
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Decrement
		val, err := store.Decrement(ctx, key, 3)
		if err != nil {
			t.Fatalf("Decrement failed: %v", err)
		}
		if val != 7 {
			t.Errorf("Expected 7, got %d", val)
		}
	})

	t.Run("CompareAndSwap", func(t *testing.T) {
		key := "cas-test"
		oldValue := []byte("old")
		newValue := []byte("new")

		// CAS on non-existent key
		err := store.CompareAndSwap(ctx, key, oldValue, newValue)
		if !errors.Is(err, ErrConflict) {
			t.Errorf("Expected ErrConflict for non-existent key, got %v", err)
		}

		// Set initial value
		err = store.Set(ctx, key, oldValue)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// CAS with wrong old value
		err = store.CompareAndSwap(ctx, key, []byte("wrong"), newValue)
		if !errors.Is(err, ErrConflict) {
			t.Errorf("Expected ErrConflict for wrong old value, got %v", err)
		}

		// CAS with correct old value
		err = store.CompareAndSwap(ctx, key, oldValue, newValue)
		if err != nil {
			t.Fatalf("CompareAndSwap failed: %v", err)
		}

		// Verify new value
		got, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if string(got) != string(newValue) {
			t.Errorf("Expected %s, got %s", newValue, got)
		}

		// CAS delete (new = nil)
		err = store.CompareAndSwap(ctx, key, newValue, nil)
		if err != nil {
			t.Fatalf("CompareAndSwap delete failed: %v", err)
		}

		// Verify deleted
		_, err = store.Get(ctx, key)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound after CAS delete, got %v", err)
		}
	})
}

func TestMockStore_BatchOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	defer store.Close()

	t.Run("BatchSet and BatchGet", func(t *testing.T) {
		items := map[string][]byte{
			"batch-1": []byte("value-1"),
			"batch-2": []byte("value-2"),
			"batch-3": []byte("value-3"),
		}

		// Batch set
		err := store.BatchSet(ctx, items)
		if err != nil {
			t.Fatalf("BatchSet failed: %v", err)
		}

		// Batch get
		keys := []string{"batch-1", "batch-2", "batch-3", "non-existent"}
		results, err := store.BatchGet(ctx, keys)
		if err != nil {
			t.Fatalf("BatchGet failed: %v", err)
		}

		// Verify results
		if len(results) != 3 {
			t.Errorf("Expected 3 results, got %d", len(results))
		}

		for key, expectedValue := range items {
			if got, ok := results[key]; !ok {
				t.Errorf("Key %s not found in results", key)
			} else if string(got) != string(expectedValue) {
				t.Errorf("Key %s: expected %s, got %s", key, expectedValue, got)
			}
		}

		// Non-existent key should not be in results
		if _, ok := results["non-existent"]; ok {
			t.Error("Non-existent key should not be in results")
		}
	})

	t.Run("BatchDelete", func(t *testing.T) {
		// Set some values
		keys := []string{"del-1", "del-2", "del-3"}
		for _, key := range keys {
			err := store.Set(ctx, key, []byte("value"))
			if err != nil {
				t.Fatalf("Set failed: %v", err)
			}
		}

		// Batch delete
		err := store.BatchDelete(ctx, keys)
		if err != nil {
			t.Fatalf("BatchDelete failed: %v", err)
		}

		// Verify all deleted
		for _, key := range keys {
			_, err := store.Get(ctx, key)
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("Key %s should be deleted, got error %v", key, err)
			}
		}
	})

	t.Run("BatchSetWithTTL", func(t *testing.T) {
		items := map[string][]byte{
			"ttl-batch-1": []byte("value-1"),
			"ttl-batch-2": []byte("value-2"),
		}
		ttl := 100 * time.Millisecond

		// Batch set with TTL
		err := store.BatchSetWithTTL(ctx, items, ttl)
		if err != nil {
			t.Fatalf("BatchSetWithTTL failed: %v", err)
		}

		// Values should exist
		for key := range items {
			exists, err := store.Exists(ctx, key)
			if err != nil {
				t.Fatalf("Exists failed: %v", err)
			}
			if !exists {
				t.Errorf("Key %s should exist", key)
			}
		}

		// Wait for expiration (check any key)
		var anyKey string
		for k := range items {
			anyKey = k
			break
		}
		waitForExpiration(t, store, anyKey, ttl+100*time.Millisecond)

		// Values should be gone
		for key := range items {
			exists, err := store.Exists(ctx, key)
			if err != nil {
				t.Fatalf("Exists failed: %v", err)
			}
			if exists {
				t.Errorf("Key %s should be expired", key)
			}
		}
	})
}

func TestMockStore_Transaction(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	defer store.Close()

	t.Run("Basic transaction operations", func(t *testing.T) {
		// Set initial value
		err := store.Set(ctx, "tx-key", []byte("initial"))
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Begin transaction
		tx, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}

		// Operations within transaction
		err = tx.Set("tx-key", []byte("updated"))
		if err != nil {
			t.Fatalf("Transaction Set failed: %v", err)
		}

		err = tx.Set("new-key", []byte("new-value"))
		if err != nil {
			t.Fatalf("Transaction Set failed: %v", err)
		}

		// Read within transaction should see pending changes
		val, err := tx.Get("tx-key")
		if err != nil {
			t.Fatalf("Transaction Get failed: %v", err)
		}
		if string(val) != "updated" {
			t.Errorf("Transaction should see pending changes, got %s", val)
		}

		// Store should not see changes yet
		val, err = store.Get(ctx, "tx-key")
		if err != nil {
			t.Fatalf("Store Get failed: %v", err)
		}
		if string(val) != "initial" {
			t.Errorf("Store should not see uncommitted changes, got %s", val)
		}

		// Commit transaction
		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		// Now store should see changes
		val, err = store.Get(ctx, "tx-key")
		if err != nil {
			t.Fatalf("Store Get after commit failed: %v", err)
		}
		if string(val) != "updated" {
			t.Errorf("Store should see committed changes, got %s", val)
		}

		val, err = store.Get(ctx, "new-key")
		if err != nil {
			t.Fatalf("Store Get new key failed: %v", err)
		}
		if string(val) != "new-value" {
			t.Errorf("Expected new-value, got %s", val)
		}
	})

	t.Run("Transaction rollback", func(t *testing.T) {
		// Set initial value
		err := store.Set(ctx, "rollback-key", []byte("initial"))
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Begin transaction
		tx, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}

		// Make changes
		err = tx.Set("rollback-key", []byte("changed"))
		if err != nil {
			t.Fatalf("Transaction Set failed: %v", err)
		}

		// Rollback
		err = tx.Rollback()
		if err != nil {
			t.Fatalf("Rollback failed: %v", err)
		}

		// Store should not see changes
		val, err := store.Get(ctx, "rollback-key")
		if err != nil {
			t.Fatalf("Store Get after rollback failed: %v", err)
		}
		if string(val) != "initial" {
			t.Errorf("Store should not see rolled back changes, got %s", val)
		}
	})

	t.Run("Transaction with TTL", func(t *testing.T) {
		tx, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}

		// Set with TTL in transaction
		err = tx.SetWithTTL("tx-ttl-key", []byte("ttl-value"), 100*time.Millisecond)
		if err != nil {
			t.Fatalf("Transaction SetWithTTL failed: %v", err)
		}

		// Commit
		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		// Value should exist
		exists, err := store.Exists(ctx, "tx-ttl-key")
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Error("Key should exist after commit")
		}

		// Wait for expiration
		time.Sleep(110 * time.Millisecond)

		// Value should be gone
		exists, err = store.Exists(ctx, "tx-ttl-key")
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if exists {
			t.Error("Key should be expired")
		}
	})

	t.Run("Transaction increment", func(t *testing.T) {
		// Set initial counter
		err := store.Set(ctx, "tx-counter", SerializeInt64(10))
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		tx, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}

		// Increment in transaction
		val, err := tx.Increment("tx-counter", 5)
		if err != nil {
			t.Fatalf("Transaction Increment failed: %v", err)
		}
		if val != 15 {
			t.Errorf("Expected 15, got %d", val)
		}

		// Increment again
		val, err = tx.Increment("tx-counter", 3)
		if err != nil {
			t.Fatalf("Transaction Increment failed: %v", err)
		}
		if val != 18 {
			t.Errorf("Expected 18, got %d", val)
		}

		// Commit
		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		// Verify final value
		data, err := store.Get(ctx, "tx-counter")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		finalVal, err := DeserializeInt64(data)
		if err != nil {
			t.Fatalf("DeserializeInt64 failed: %v", err)
		}
		if finalVal != 18 {
			t.Errorf("Expected final value 18, got %d", finalVal)
		}
	})
}

func TestMockStore_ScanOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	defer store.Close()

	// Set up test data
	testData := map[string][]byte{
		"user:1":      []byte("alice"),
		"user:2":      []byte("bob"),
		"user:3":      []byte("charlie"),
		"config:app":  []byte("settings"),
		"config:db":   []byte("connection"),
		"cache:page1": []byte("content"),
	}

	for key, value := range testData {
		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	t.Run("ScanWithPrefix", func(t *testing.T) {
		// Scan user keys
		keys, err := store.ScanWithPrefix(ctx, "user:", 10)
		if err != nil {
			t.Fatalf("ScanWithPrefix failed: %v", err)
		}
		if len(keys) != 3 {
			t.Errorf("Expected 3 user keys, got %d", len(keys))
		}

		// Scan config keys
		keys, err = store.ScanWithPrefix(ctx, "config:", 10)
		if err != nil {
			t.Fatalf("ScanWithPrefix failed: %v", err)
		}
		if len(keys) != 2 {
			t.Errorf("Expected 2 config keys, got %d", len(keys))
		}

		// Test limit
		keys, err = store.ScanWithPrefix(ctx, "user:", 2)
		if err != nil {
			t.Fatalf("ScanWithPrefix with limit failed: %v", err)
		}
		if len(keys) != 2 {
			t.Errorf("Expected 2 keys with limit, got %d", len(keys))
		}
	})

	t.Run("Scan with pattern", func(t *testing.T) {
		// Simple pattern matching (contains)
		keys, err := store.Scan(ctx, "user", 10)
		if err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		if len(keys) != 3 {
			t.Errorf("Expected 3 keys containing 'user', got %d", len(keys))
		}

		// Wildcard pattern
		keys, err = store.Scan(ctx, "*", 10)
		if err != nil {
			t.Fatalf("Scan with wildcard failed: %v", err)
		}
		if len(keys) != 6 {
			t.Errorf("Expected all 6 keys, got %d", len(keys))
		}
	})

	t.Run("Scan with expired keys", func(t *testing.T) {
		// Add key with short TTL
		err := store.SetWithTTL(ctx, "temp:1", []byte("temporary"), 50*time.Millisecond)
		if err != nil {
			t.Fatalf("SetWithTTL failed: %v", err)
		}

		// Should find it immediately
		keys, err := store.ScanWithPrefix(ctx, "temp:", 10)
		if err != nil {
			t.Fatalf("ScanWithPrefix failed: %v", err)
		}
		if len(keys) != 1 {
			t.Errorf("Expected 1 temp key, got %d", len(keys))
		}

		// Wait for expiration
		time.Sleep(60 * time.Millisecond)

		// Should not find expired key
		keys, err = store.ScanWithPrefix(ctx, "temp:", 10)
		if err != nil {
			t.Fatalf("ScanWithPrefix after expiration failed: %v", err)
		}
		if len(keys) != 0 {
			t.Errorf("Expected 0 temp keys after expiration, got %d", len(keys))
		}
	})
}

func TestMockStore_EdgeCases(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()

	t.Run("Operations on closed store", func(t *testing.T) {
		// Close the store
		err := store.Close()
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		// Try operations
		err = store.Set(ctx, "key", []byte("value"))
		if !errors.Is(err, ErrStorageClosed) {
			t.Errorf("Expected ErrStorageClosed on Set, got %v", err)
		}

		_, err = store.Get(ctx, "key")
		if !errors.Is(err, ErrStorageClosed) {
			t.Errorf("Expected ErrStorageClosed on Get, got %v", err)
		}

		err = store.Ping(ctx)
		if !errors.Is(err, ErrStorageClosed) {
			t.Errorf("Expected ErrStorageClosed on Ping, got %v", err)
		}

		// Close again should also return error
		err = store.Close()
		if !errors.Is(err, ErrStorageClosed) {
			t.Errorf("Expected ErrStorageClosed on second Close, got %v", err)
		}
	})

	t.Run("Invalid key operations", func(t *testing.T) {
		store := NewMockStore()
		defer store.Close()

		// Empty key
		err := store.Set(ctx, "", []byte("value"))
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Expected ErrInvalidKey for empty key, got %v", err)
		}

		// Empty key in batch
		err = store.BatchSet(ctx, map[string][]byte{"": []byte("value")})
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Expected ErrInvalidKey for empty key in batch, got %v", err)
		}
	})

	t.Run("Context cancellation", func(t *testing.T) {
		store := NewMockStore()
		defer store.Close()

		// Create cancelled context
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		// Operations should fail with timeout
		err := store.Set(cancelCtx, "key", []byte("value"))
		if !errors.Is(err, ErrTimeout) {
			t.Errorf("Expected ErrTimeout with cancelled context, got %v", err)
		}

		_, err = store.Get(cancelCtx, "key")
		if !errors.Is(err, ErrTimeout) {
			t.Errorf("Expected ErrTimeout with cancelled context, got %v", err)
		}
	})

	t.Run("Data isolation", func(t *testing.T) {
		store := NewMockStore()
		defer store.Close()

		// Set value
		original := []byte("original")
		err := store.Set(ctx, "key", original)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Modify original slice
		original[0] = 'X'

		// Get value - should not be affected
		got, err := store.Get(ctx, "key")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if string(got) != "original" {
			t.Errorf("Store should maintain data isolation, got %s", got)
		}

		// Modify returned slice
		got[0] = 'Y'

		// Get again - should still be original
		got2, err := store.Get(ctx, "key")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if string(got2) != "original" {
			t.Errorf("Store should maintain data isolation, got %s", got2)
		}
	})
}

func TestMockStore_ConcurrentOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	defer store.Close()

	// Run concurrent operations
	done := make(chan bool)
	errs := make(chan error, 100)

	// Writers
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				key := "key"
				value := []byte(string(rune('A' + id)))
				if err := store.Set(ctx, key, value); err != nil {
					select {
					case errs <- err:
					default:
					}
				}
			}
			done <- true
		}(i)
	}

	// Readers
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				if _, err := store.Get(ctx, "key"); err != nil && !errors.Is(err, ErrNotFound) {
					select {
					case errs <- err:
					default:
					}
				}
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Check for errors
	close(errs)
	for err := range errs {
		t.Errorf("Concurrent operation error: %v", err)
	}
}
