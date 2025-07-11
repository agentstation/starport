package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/testutil"
)

// TestBadgerStore runs all BadgerStore tests
func TestBadgerStore(t *testing.T) {
	t.Parallel()

	t.Run("BasicOperations", testBadgerBasicOperations)
	t.Run("TTLOperations", testBadgerTTLOperations)
	t.Run("AtomicOperations", testBadgerAtomicOperations)
	t.Run("BatchOperations", testBadgerBatchOperations)
	t.Run("Transactions", testBadgerTransactions)
	t.Run("ScanOperations", testBadgerScanOperations)
	t.Run("BackupRestore", testBadgerBackupRestore)
	t.Run("Concurrency", testBadgerConcurrency)
	t.Run("EdgeCases", testBadgerEdgeCases)
	t.Run("Performance", testBadgerPerformance)
}

// createTestBadgerStore creates a BadgerStore for testing
func createTestBadgerStore(t *testing.T) (*BadgerStore, func()) {
	t.Helper()

	dir := filepath.Join(os.TempDir(), fmt.Sprintf("badger_test_%d_%d", time.Now().UnixNano(), os.Getpid()))

	config := BadgerConfig{
		Path:         dir,
		SyncWrites:   false,
		Compression:  true,
		NumVersions:  1,
		NumLevelZero: 5,
		MemTableSize: 64 << 20, // 64MB
	}

	store, err := OpenBadger(config)
	if err != nil {
		t.Fatalf("Failed to create BadgerStore: %v", err)
	}

	cleanup := func() {
		store.Close()
		os.RemoveAll(dir)
	}

	return store, cleanup
}

func testBadgerBasicOperations(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Test Set and Get
	t.Run("SetAndGet", func(t *testing.T) {
		key := "test_key"
		value := []byte("test_value")

		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		retrieved, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if string(retrieved) != string(value) {
			t.Errorf("Expected %s, got %s", value, retrieved)
		}
	})

	// Test Exists
	t.Run("Exists", func(t *testing.T) {
		key := "exists_key"
		value := []byte("exists_value")

		exists, err := store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if exists {
			t.Errorf("Key should not exist yet")
		}

		err = store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		exists, err = store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Errorf("Key should exist")
		}
	})

	// Test Delete
	t.Run("Delete", func(t *testing.T) {
		key := "delete_key"
		value := []byte("delete_value")

		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		err = store.Delete(ctx, key)
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		_, err = store.Get(ctx, key)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound, got %v", err)
		}
	})

	// Test invalid key
	t.Run("InvalidKey", func(t *testing.T) {
		err := store.Set(ctx, "", []byte("value"))
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Expected ErrInvalidKey, got %v", err)
		}

		_, err = store.Get(ctx, "")
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Expected ErrInvalidKey, got %v", err)
		}
	})

	// Test not found
	t.Run("NotFound", func(t *testing.T) {
		_, err := store.Get(ctx, "nonexistent_key")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound, got %v", err)
		}
	})
}

func testBadgerTTLOperations(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Test SetWithTTL
	t.Run("SetWithTTL", func(t *testing.T) {
		key := "ttl_key"
		value := []byte("ttl_value")
		ttl := 2 * time.Second // Badger has minimum TTL around 1 second

		err := store.SetWithTTL(ctx, key, value, ttl)
		if err != nil {
			t.Fatalf("SetWithTTL failed: %v", err)
		}

		// Key should exist immediately
		retrieved, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if string(retrieved) != string(value) {
			t.Errorf("Expected %s, got %s", value, retrieved)
		}

		// Wait for expiration (with buffer)
		testutil.WaitForExpiration(t, store, key, ttl+500*time.Millisecond)

		// Key should be expired - Badger marks it as expired even if not yet garbage collected
		_, err = store.Get(ctx, key)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound for expired key, got %v", err)
		}
	})

	// Test GetTTL
	t.Run("GetTTL", func(t *testing.T) {
		key := "ttl_check_key"
		value := []byte("ttl_check_value")
		ttl := 5 * time.Second

		err := store.SetWithTTL(ctx, key, value, ttl)
		if err != nil {
			t.Fatalf("SetWithTTL failed: %v", err)
		}

		remainingTTL, err := store.GetTTL(ctx, key)
		if err != nil {
			t.Fatalf("GetTTL failed: %v", err)
		}

		// TTL should be close to what we set (allow variance for execution time)
		// Badger has per-second granularity, so allow up to 1 second variance
		if remainingTTL > ttl || remainingTTL < ttl-1*time.Second {
			t.Errorf("TTL mismatch: expected ~%v, got %v", ttl, remainingTTL)
		}

		// Test key without TTL
		noTTLKey := "no_ttl_key"
		err = store.Set(ctx, noTTLKey, []byte("value"))
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		remainingTTL, err = store.GetTTL(ctx, noTTLKey)
		if err != nil {
			t.Fatalf("GetTTL failed: %v", err)
		}
		if remainingTTL != 0 {
			t.Errorf("Expected 0 TTL for key without expiration, got %v", remainingTTL)
		}
	})

	// Test ExpireAt
	t.Run("ExpireAt", func(t *testing.T) {
		key := "expire_at_key"
		value := []byte("expire_at_value")

		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Set expiration to 2 seconds from now
		expireAt := time.Now().Add(2 * time.Second)
		err = store.ExpireAt(ctx, key, expireAt)
		if err != nil {
			t.Fatalf("ExpireAt failed: %v", err)
		}

		// Wait a moment to ensure operation completes
		testutil.WaitFor(t, func() bool {
			// The key should still exist since we set a future expiration
			exists, _ := store.Exists(ctx, key)
			return exists
		}, 100*time.Millisecond, "ExpireAt to complete")

		// Check - key should still exist
		exists, err := store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Errorf("Key should exist before expiration")
		}

		// Wait for expiration
		testutil.WaitForKeyNotExists(t, store, key, 3*time.Second)

		// Key should be expired
		exists, err = store.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if exists {
			t.Errorf("Key should not exist after expiration")
		}
	})
}

func testBadgerAtomicOperations(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Test Increment
	t.Run("Increment", func(t *testing.T) {
		key := "counter_key"

		// First increment (key doesn't exist)
		newValue, err := store.Increment(ctx, key, 5)
		if err != nil {
			t.Fatalf("Increment failed: %v", err)
		}
		if newValue != 5 {
			t.Errorf("Expected 5, got %d", newValue)
		}

		// Second increment
		newValue, err = store.Increment(ctx, key, 3)
		if err != nil {
			t.Fatalf("Increment failed: %v", err)
		}
		if newValue != 8 {
			t.Errorf("Expected 8, got %d", newValue)
		}

		// Verify the stored value
		data, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		value, err := DeserializeInt64(data)
		if err != nil {
			t.Fatalf("DeserializeInt64 failed: %v", err)
		}
		if value != 8 {
			t.Errorf("Expected stored value 8, got %d", value)
		}
	})

	// Test Decrement
	t.Run("Decrement", func(t *testing.T) {
		key := "decrement_key"

		// Set initial value
		_, err := store.Increment(ctx, key, 10)
		if err != nil {
			t.Fatalf("Initial increment failed: %v", err)
		}

		// Decrement
		newValue, err := store.Decrement(ctx, key, 3)
		if err != nil {
			t.Fatalf("Decrement failed: %v", err)
		}
		if newValue != 7 {
			t.Errorf("Expected 7, got %d", newValue)
		}
	})

	// Test Increment with TTL preservation
	t.Run("IncrementWithTTL", func(t *testing.T) {
		key := "ttl_counter_key"
		ttl := 5 * time.Second

		// Set initial value with TTL
		serialized := SerializeInt64(10)
		err := store.SetWithTTL(ctx, key, serialized, ttl)
		if err != nil {
			t.Fatalf("SetWithTTL failed: %v", err)
		}

		// Increment should preserve TTL
		_, err = store.Increment(ctx, key, 5)
		if err != nil {
			t.Fatalf("Increment failed: %v", err)
		}

		// Check TTL is still set
		remainingTTL, err := store.GetTTL(ctx, key)
		if err != nil {
			t.Fatalf("GetTTL failed: %v", err)
		}
		if remainingTTL <= 0 || remainingTTL > ttl {
			t.Errorf("TTL not preserved: %v", remainingTTL)
		}
	})

	// Test CompareAndSwap
	t.Run("CompareAndSwap", func(t *testing.T) {
		key := "cas_key"
		oldValue := []byte("old_value")
		newValue := []byte("new_value")

		// Set initial value
		err := store.Set(ctx, key, oldValue)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Successful CAS
		err = store.CompareAndSwap(ctx, key, oldValue, newValue)
		if err != nil {
			t.Fatalf("CompareAndSwap failed: %v", err)
		}

		// Verify new value
		retrieved, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if string(retrieved) != string(newValue) {
			t.Errorf("Expected %s, got %s", newValue, retrieved)
		}

		// Failed CAS (wrong old value)
		wrongOld := []byte("wrong_value")
		err = store.CompareAndSwap(ctx, key, wrongOld, oldValue)
		if !errors.Is(err, ErrConflict) {
			t.Errorf("Expected ErrConflict, got %v", err)
		}

		// CAS on non-existent key with nil old value
		nonExistentKey := "cas_new_key"
		err = store.CompareAndSwap(ctx, nonExistentKey, nil, newValue)
		if err != nil {
			t.Fatalf("CompareAndSwap on new key failed: %v", err)
		}

		// Verify it was set
		retrieved, err = store.Get(ctx, nonExistentKey)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if string(retrieved) != string(newValue) {
			t.Errorf("Expected %s, got %s", newValue, retrieved)
		}
	})
}

func testBadgerBatchOperations(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Test BatchSet and BatchGet
	t.Run("BatchSetAndGet", func(t *testing.T) {
		items := map[string][]byte{
			"batch_key1": []byte("value1"),
			"batch_key2": []byte("value2"),
			"batch_key3": []byte("value3"),
		}

		// Batch set
		err := store.BatchSet(ctx, items)
		if err != nil {
			t.Fatalf("BatchSet failed: %v", err)
		}

		// Batch get
		keys := []string{"batch_key1", "batch_key2", "batch_key3", "nonexistent"}
		results, err := store.BatchGet(ctx, keys)
		if err != nil {
			t.Fatalf("BatchGet failed: %v", err)
		}

		// Verify results
		if len(results) != 3 {
			t.Errorf("Expected 3 results, got %d", len(results))
		}
		for key, expectedValue := range items {
			if value, ok := results[key]; !ok {
				t.Errorf("Key %s not found in results", key)
			} else if string(value) != string(expectedValue) {
				t.Errorf("For key %s, expected %s, got %s", key, expectedValue, value)
			}
		}
		if _, ok := results["nonexistent"]; ok {
			t.Errorf("Nonexistent key should not be in results")
		}
	})

	// Test BatchDelete
	t.Run("BatchDelete", func(t *testing.T) {
		// Set up keys
		items := map[string][]byte{
			"delete_key1": []byte("value1"),
			"delete_key2": []byte("value2"),
			"delete_key3": []byte("value3"),
		}
		err := store.BatchSet(ctx, items)
		if err != nil {
			t.Fatalf("BatchSet failed: %v", err)
		}

		// Batch delete
		keys := []string{"delete_key1", "delete_key2", "nonexistent"}
		err = store.BatchDelete(ctx, keys)
		if err != nil {
			t.Fatalf("BatchDelete failed: %v", err)
		}

		// Verify deletions
		exists1, _ := store.Exists(ctx, "delete_key1")
		exists2, _ := store.Exists(ctx, "delete_key2")
		exists3, _ := store.Exists(ctx, "delete_key3")

		if exists1 || exists2 {
			t.Errorf("Deleted keys should not exist")
		}
		if !exists3 {
			t.Errorf("Non-deleted key should still exist")
		}
	})

	// Test BatchSetWithTTL
	t.Run("BatchSetWithTTL", func(t *testing.T) {
		items := map[string][]byte{
			"ttl_batch_key1": []byte("value1"),
			"ttl_batch_key2": []byte("value2"),
		}
		ttl := 2 * time.Second // Badger has minimum TTL around 1 second

		err := store.BatchSetWithTTL(ctx, items, ttl)
		if err != nil {
			t.Fatalf("BatchSetWithTTL failed: %v", err)
		}

		// Keys should exist immediately
		results, err := store.BatchGet(ctx, []string{"ttl_batch_key1", "ttl_batch_key2"})
		if err != nil {
			t.Fatalf("BatchGet failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(results))
		}

		// Wait for expiration - Badger's IsDeletedOrExpired checks expiration time
		time.Sleep(ttl + 50*time.Millisecond)

		// Keys should be expired - BatchGet should not return expired keys
		results, err = store.BatchGet(ctx, []string{"ttl_batch_key1", "ttl_batch_key2"})
		if err != nil {
			t.Fatalf("BatchGet failed: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("Expected 0 results after expiration, got %d", len(results))
		}
	})
}

func testBadgerTransactions(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Test basic transaction operations
	t.Run("BasicTransaction", func(t *testing.T) {
		txn, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}

		// Set within transaction
		err = txn.Set("txn_key1", []byte("value1"))
		if err != nil {
			t.Fatalf("Transaction Set failed: %v", err)
		}

		// Get within transaction (should see the uncommitted value)
		value, err := txn.Get("txn_key1")
		if err != nil {
			t.Fatalf("Transaction Get failed: %v", err)
		}
		if string(value) != "value1" {
			t.Errorf("Expected value1, got %s", value)
		}

		// Value shouldn't be visible outside transaction yet
		_, err = store.Get(ctx, "txn_key1")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound outside transaction, got %v", err)
		}

		// Commit transaction
		err = txn.Commit(ctx)
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		// Now value should be visible
		value, err = store.Get(ctx, "txn_key1")
		if err != nil {
			t.Fatalf("Get after commit failed: %v", err)
		}
		if string(value) != "value1" {
			t.Errorf("Expected value1 after commit, got %s", value)
		}
	})

	// Test transaction rollback
	t.Run("TransactionRollback", func(t *testing.T) {
		txn, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}

		// Make changes in transaction
		err = txn.Set("rollback_key", []byte("should_not_persist"))
		if err != nil {
			t.Fatalf("Transaction Set failed: %v", err)
		}

		// Rollback transaction
		err = txn.Rollback()
		if err != nil {
			t.Fatalf("Rollback failed: %v", err)
		}

		// Value should not exist
		_, err = store.Get(ctx, "rollback_key")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Expected ErrNotFound after rollback, got %v", err)
		}
	})

	// Test transaction with TTL
	t.Run("TransactionWithTTL", func(t *testing.T) {
		txn, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}

		ttl := 5 * time.Second
		err = txn.SetWithTTL("txn_ttl_key", []byte("ttl_value"), ttl)
		if err != nil {
			t.Fatalf("Transaction SetWithTTL failed: %v", err)
		}

		err = txn.Commit(ctx)
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		// Check TTL was set
		remainingTTL, err := store.GetTTL(ctx, "txn_ttl_key")
		if err != nil {
			t.Fatalf("GetTTL failed: %v", err)
		}
		if remainingTTL <= 0 || remainingTTL > ttl {
			t.Errorf("TTL not set correctly: %v", remainingTTL)
		}
	})

	// Test transaction increment
	t.Run("TransactionIncrement", func(t *testing.T) {
		// Set initial value
		serialized := SerializeInt64(10)
		err := store.Set(ctx, "txn_counter", serialized)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		txn, err := store.BeginTransaction(ctx)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}

		newValue, err := txn.Increment("txn_counter", 5)
		if err != nil {
			t.Fatalf("Transaction Increment failed: %v", err)
		}
		if newValue != 15 {
			t.Errorf("Expected 15, got %d", newValue)
		}

		err = txn.Commit(ctx)
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		// Verify committed value
		data, _ := store.Get(ctx, "txn_counter")
		value, _ := DeserializeInt64(data)
		if value != 15 {
			t.Errorf("Expected committed value 15, got %d", value)
		}
	})
}

func testBadgerScanOperations(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Set up test data
	testData := map[string][]byte{
		"prefix:key1":  []byte("value1"),
		"prefix:key2":  []byte("value2"),
		"prefix:key3":  []byte("value3"),
		"other:key1":   []byte("value4"),
		"another:key1": []byte("value5"),
	}

	for key, value := range testData {
		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed for %s: %v", key, err)
		}
	}

	// Test ScanWithPrefix
	t.Run("ScanWithPrefix", func(t *testing.T) {
		keys, err := store.ScanWithPrefix(ctx, "prefix:", 10)
		if err != nil {
			t.Fatalf("ScanWithPrefix failed: %v", err)
		}

		if len(keys) != 3 {
			t.Errorf("Expected 3 keys with prefix, got %d", len(keys))
		}

		// Verify all keys have the prefix
		for _, key := range keys {
			if len(key) < 7 || key[:7] != "prefix:" {
				t.Errorf("Key %s doesn't have expected prefix", key)
			}
		}

		// Test with limit
		keys, err = store.ScanWithPrefix(ctx, "prefix:", 2)
		if err != nil {
			t.Fatalf("ScanWithPrefix with limit failed: %v", err)
		}
		if len(keys) != 2 {
			t.Errorf("Expected 2 keys with limit, got %d", len(keys))
		}
	})

	// Test Scan with pattern
	t.Run("ScanWithPattern", func(t *testing.T) {
		// All keys
		keys, err := store.Scan(ctx, "*", 0)
		if err != nil {
			t.Fatalf("Scan all failed: %v", err)
		}
		if len(keys) != 5 {
			t.Errorf("Expected 5 total keys, got %d", len(keys))
		}

		// Pattern matching (prefix with wildcard)
		keys, err = store.Scan(ctx, "prefix:*", 0)
		if err != nil {
			t.Fatalf("Scan with pattern failed: %v", err)
		}
		if len(keys) != 3 {
			t.Errorf("Expected 3 keys matching pattern, got %d", len(keys))
		}
	})

	// Test scan with expired keys
	t.Run("ScanIgnoresExpiredKeys", func(t *testing.T) {
		// Add a key with short TTL (at least 1 second for Badger)
		err := store.SetWithTTL(ctx, "prefix:expiring", []byte("expiring"), 1*time.Second)
		if err != nil {
			t.Fatalf("SetWithTTL failed: %v", err)
		}

		// Scan should include it immediately
		keys, err := store.ScanWithPrefix(ctx, "prefix:", 10)
		if err != nil {
			t.Fatalf("ScanWithPrefix failed: %v", err)
		}
		if len(keys) != 4 {
			t.Errorf("Expected 4 keys before expiration, got %d", len(keys))
		}

		// Wait for expiration
		time.Sleep(1100 * time.Millisecond)

		// Scan should not include expired key - IsDeletedOrExpired should filter it out
		keys, err = store.ScanWithPrefix(ctx, "prefix:", 10)
		if err != nil {
			t.Fatalf("ScanWithPrefix after expiration failed: %v", err)
		}
		if len(keys) != 3 {
			t.Errorf("Expected 3 keys after expiration, got %d", len(keys))
		}
	})
}

func testBadgerBackupRestore(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Set up test data
	testData := map[string][]byte{
		"backup_key1": []byte("value1"),
		"backup_key2": []byte("value2"),
		"backup_key3": []byte("value3"),
	}

	for key, value := range testData {
		err := store.Set(ctx, key, value)
		if err != nil {
			t.Fatalf("Set failed for %s: %v", key, err)
		}
	}

	// Create backup
	backupPath := filepath.Join(os.TempDir(), fmt.Sprintf("badger_backup_%d.bak", time.Now().UnixNano()))
	defer os.Remove(backupPath)

	err := store.Backup(ctx, backupPath)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup file exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Fatalf("Backup file was not created")
	}

	// Clear the store
	for key := range testData {
		err := store.Delete(ctx, key)
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
	}

	// Verify data is gone
	for key := range testData {
		_, err := store.Get(ctx, key)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Key %s should not exist after deletion", key)
		}
	}

	// Restore from backup
	err = store.Restore(ctx, backupPath)
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify data is restored
	for key, expectedValue := range testData {
		value, err := store.Get(ctx, key)
		if err != nil {
			t.Errorf("Failed to get restored key %s: %v", key, err)
		}
		if string(value) != string(expectedValue) {
			t.Errorf("Restored value mismatch for %s: expected %s, got %s", key, expectedValue, value)
		}
	}
}

func testBadgerConcurrency(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Test concurrent reads and writes
	t.Run("ConcurrentReadWrite", func(t *testing.T) {
		var wg sync.WaitGroup
		numGoroutines := 10
		numOperations := 100

		// Concurrent writers
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					key := fmt.Sprintf("concurrent_key_%d_%d", id, j)
					value := fmt.Sprintf("value_%d_%d", id, j)
					err := store.Set(ctx, key, []byte(value))
					if err != nil {
						t.Errorf("Set failed: %v", err)
					}
				}
			}(i)
		}

		// Concurrent readers
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					key := fmt.Sprintf("concurrent_key_%d_%d", id, j)
					// Some reads might fail if write hasn't happened yet
					store.Get(ctx, key)
				}
			}(i)
		}

		wg.Wait()

		// Verify all writes succeeded
		for i := 0; i < numGoroutines; i++ {
			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("concurrent_key_%d_%d", i, j)
				expectedValue := fmt.Sprintf("value_%d_%d", i, j)
				value, err := store.Get(ctx, key)
				if err != nil {
					t.Errorf("Failed to get key %s: %v", key, err)
				}
				if string(value) != expectedValue {
					t.Errorf("Value mismatch for %s", key)
				}
			}
		}
	})

	// Test concurrent increments
	t.Run("ConcurrentIncrement", func(t *testing.T) {
		key := "concurrent_counter"
		var wg sync.WaitGroup
		// Reduce contention for race detector
		numGoroutines := 10
		incrementsPerGoroutine := 20

		var successCount atomic.Int64

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < incrementsPerGoroutine; j++ {
					_, err := store.Increment(ctx, key, 1)
					if err != nil {
						// Log but don't fail - some conflicts are expected under extreme contention
						t.Logf("Increment failed after retries: %v", err)
					} else {
						successCount.Add(1)
					}
				}
			}()
		}

		wg.Wait()

		// Verify final count
		data, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		finalCount, err := DeserializeInt64(data)
		if err != nil {
			t.Fatalf("DeserializeInt64 failed: %v", err)
		}

		// The count should match successful increments
		success := successCount.Load()
		if finalCount != success {
			t.Errorf("Expected count %d to match successful increments %d", finalCount, success)
		}

		// We should have at least 80% success rate even under extreme contention
		expectedMinSuccess := int64(float64(numGoroutines*incrementsPerGoroutine) * 0.8)
		if success < expectedMinSuccess {
			t.Errorf("Too many failures: only %d/%d increments succeeded (minimum expected: %d)",
				success, numGoroutines*incrementsPerGoroutine, expectedMinSuccess)
		}
	})
}

func testBadgerEdgeCases(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Test operations after close
	t.Run("OperationsAfterClose", func(t *testing.T) {
		// Create a separate store for this test
		tempStore, tempCleanup := createTestBadgerStore(t)
		defer tempCleanup()

		// Close the store
		err := tempStore.Close()
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		// Try operations on closed store
		err = tempStore.Set(ctx, "key", []byte("value"))
		if !errors.Is(err, ErrStorageClosed) {
			t.Errorf("Expected ErrStorageClosed on Set, got %v", err)
		}

		_, err = tempStore.Get(ctx, "key")
		if !errors.Is(err, ErrStorageClosed) {
			t.Errorf("Expected ErrStorageClosed on Get, got %v", err)
		}

		err = tempStore.Ping(ctx)
		if !errors.Is(err, ErrStorageClosed) {
			t.Errorf("Expected ErrStorageClosed on Ping, got %v", err)
		}
	})

	// Test large values
	t.Run("LargeValues", func(t *testing.T) {
		key := "large_value_key"
		// Create a 1MB value
		largeValue := make([]byte, 1024*1024)
		for i := range largeValue {
			largeValue[i] = byte(i % 256)
		}

		err := store.Set(ctx, key, largeValue)
		if err != nil {
			t.Fatalf("Set large value failed: %v", err)
		}

		retrieved, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get large value failed: %v", err)
		}

		if len(retrieved) != len(largeValue) {
			t.Errorf("Large value size mismatch: expected %d, got %d", len(largeValue), len(retrieved))
		}

		// Verify content
		for i := range retrieved {
			if retrieved[i] != largeValue[i] {
				t.Errorf("Large value content mismatch at index %d", i)
				break
			}
		}
	})

	// Test empty values
	t.Run("EmptyValues", func(t *testing.T) {
		key := "empty_value_key"
		emptyValue := []byte{}

		err := store.Set(ctx, key, emptyValue)
		if err != nil {
			t.Fatalf("Set empty value failed: %v", err)
		}

		retrieved, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get empty value failed: %v", err)
		}

		if len(retrieved) != 0 {
			t.Errorf("Expected empty value, got %d bytes", len(retrieved))
		}
	})

	// Test special characters in keys
	t.Run("SpecialCharacterKeys", func(t *testing.T) {
		specialKeys := []string{
			"key with spaces",
			"key/with/slashes",
			"key:with:colons",
			"key.with.dots",
			"key-with-dashes",
			"key_with_underscores",
			"key@with@symbols",
			"key#with#hashes",
			"key%with%percents",
			"key&with&ampersands",
		}

		for _, key := range specialKeys {
			value := []byte("test_value")
			err := store.Set(ctx, key, value)
			if err != nil {
				t.Errorf("Failed to set key %q: %v", key, err)
				continue
			}

			retrieved, err := store.Get(ctx, key)
			if err != nil {
				t.Errorf("Failed to get key %q: %v", key, err)
				continue
			}

			if string(retrieved) != string(value) {
				t.Errorf("Value mismatch for key %q", key)
			}
		}
	})
}

func testBadgerPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	store, cleanup := createTestBadgerStore(t)
	defer cleanup()

	ctx := context.Background()

	// Test write performance
	t.Run("WritePerformance", func(t *testing.T) {
		numWrites := 10000
		valueSize := 1024 // 1KB values

		value := make([]byte, valueSize)
		for i := range value {
			value[i] = byte(i % 256)
		}

		start := time.Now()
		for i := 0; i < numWrites; i++ {
			key := fmt.Sprintf("perf_write_%d", i)
			err := store.Set(ctx, key, value)
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}
		}
		duration := time.Since(start)

		writesPerSecond := float64(numWrites) / duration.Seconds()
		t.Logf("Write performance: %d writes in %v (%.2f writes/sec)", numWrites, duration, writesPerSecond)

		// Expect at least 1000 writes per second
		if writesPerSecond < 1000 {
			t.Errorf("Write performance too low: %.2f writes/sec", writesPerSecond)
		}
	})

	// Test read performance
	t.Run("ReadPerformance", func(t *testing.T) {
		// First, write some data
		numKeys := 10000
		valueSize := 1024

		value := make([]byte, valueSize)
		for i := 0; i < numKeys; i++ {
			key := fmt.Sprintf("perf_read_%d", i)
			err := store.Set(ctx, key, value)
			if err != nil {
				t.Fatalf("Setup write failed: %v", err)
			}
		}

		// Now test read performance
		start := time.Now()
		for i := 0; i < numKeys; i++ {
			key := fmt.Sprintf("perf_read_%d", i)
			_, err := store.Get(ctx, key)
			if err != nil {
				t.Fatalf("Read failed: %v", err)
			}
		}
		duration := time.Since(start)

		readsPerSecond := float64(numKeys) / duration.Seconds()
		t.Logf("Read performance: %d reads in %v (%.2f reads/sec)", numKeys, duration, readsPerSecond)

		// Expect at least 5000 reads per second
		if readsPerSecond < 5000 {
			t.Errorf("Read performance too low: %.2f reads/sec", readsPerSecond)
		}
	})

	// Test batch performance
	t.Run("BatchPerformance", func(t *testing.T) {
		batchSize := 100
		numBatches := 100

		start := time.Now()
		for i := 0; i < numBatches; i++ {
			batch := make(map[string][]byte, batchSize)
			for j := 0; j < batchSize; j++ {
				key := fmt.Sprintf("batch_perf_%d_%d", i, j)
				batch[key] = []byte(fmt.Sprintf("value_%d_%d", i, j))
			}
			err := store.BatchSet(ctx, batch)
			if err != nil {
				t.Fatalf("BatchSet failed: %v", err)
			}
		}
		duration := time.Since(start)

		totalWrites := batchSize * numBatches
		writesPerSecond := float64(totalWrites) / duration.Seconds()
		t.Logf("Batch write performance: %d writes in %v (%.2f writes/sec)", totalWrites, duration, writesPerSecond)

		// Batch writes should be faster than individual writes
		if writesPerSecond < 5000 {
			t.Errorf("Batch write performance too low: %.2f writes/sec", writesPerSecond)
		}
	})
}

// TestBadgerStoreOpenError tests error handling in OpenBadger
func TestBadgerStoreOpenError(t *testing.T) {
	// Test with invalid path (read-only directory)
	config := BadgerConfig{
		Path:         "/invalid_readonly_path_that_should_not_exist",
		SyncWrites:   false,
		Compression:  true,
		NumVersions:  1,
		NumLevelZero: 5,
		MemTableSize: 64 << 20,
	}

	_, err := OpenBadger(config)
	if err == nil {
		t.Errorf("Expected error when opening Badger with invalid path")
	}
}
