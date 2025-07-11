package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValkeyStore runs integration tests against a real Valkey instance
func TestValkeyStore(t *testing.T) {
	// Skip if no Valkey URL is provided
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("Skipping Valkey integration tests: TEST_VALKEY_URL not set")
	}

	config := ValkeyConfig{
		URL:          valkeyURL,
		MaxRetries:   3,
		MinIdleConns: 1,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolTimeout:  4 * time.Second,
		DB:           15, // Use DB 15 for tests
	}

	store, err := OpenValkey(config)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Clean up test DB
	t.Cleanup(func() {
		// Note: FLUSHDB is not available in valkey-go yet, so we'll clean manually
		keys, _ := store.Scan(ctx, "test:*", 1000)
		if len(keys) > 0 {
			_ = store.BatchDelete(ctx, keys)
		}
	})

	t.Run("Basic Operations", func(t *testing.T) {
		key := "test:basic:key"
		value := []byte("test value")

		// Set
		err := store.Set(ctx, key, value)
		assert.NoError(t, err)

		// Get
		got, err := store.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, value, got)

		// Exists
		exists, err := store.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)

		// Delete
		err = store.Delete(ctx, key)
		assert.NoError(t, err)

		// Get after delete
		_, err = store.Get(ctx, key)
		assert.Equal(t, ErrNotFound, err)

		// Exists after delete
		exists, err = store.Exists(ctx, key)
		assert.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("TTL Operations", func(t *testing.T) {
		key := "test:ttl:key"
		value := []byte("ttl value")

		// Set with TTL
		err := store.SetWithTTL(ctx, key, value, 2*time.Second)
		assert.NoError(t, err)

		// Get TTL
		ttl, err := store.GetTTL(ctx, key)
		assert.NoError(t, err)
		assert.Greater(t, ttl, time.Duration(0))
		assert.LessOrEqual(t, ttl, 2*time.Second)

		// Value should exist
		got, err := store.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, value, got)

		// Wait for expiration
		time.Sleep(3 * time.Second)

		// Value should be gone
		_, err = store.Get(ctx, key)
		assert.Equal(t, ErrNotFound, err)
	})

	t.Run("ExpireAt", func(t *testing.T) {
		key := "test:expireat:key"
		value := []byte("expire value")

		// Set without TTL
		err := store.Set(ctx, key, value)
		assert.NoError(t, err)

		// Set expiration
		expireAt := time.Now().Add(1 * time.Second)
		err = store.ExpireAt(ctx, key, expireAt)
		assert.NoError(t, err)

		// Value should exist
		exists, err := store.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)

		// Wait for expiration
		time.Sleep(2 * time.Second)

		// Value should be gone
		exists, err = store.Exists(ctx, key)
		assert.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Atomic Operations", func(t *testing.T) {
		key := "test:atomic:counter"

		// Increment on non-existent key
		val, err := store.Increment(ctx, key, 5)
		assert.NoError(t, err)
		assert.Equal(t, int64(5), val)

		// Increment again
		val, err = store.Increment(ctx, key, 3)
		assert.NoError(t, err)
		assert.Equal(t, int64(8), val)

		// Decrement
		val, err = store.Decrement(ctx, key, 2)
		assert.NoError(t, err)
		assert.Equal(t, int64(6), val)

		// Verify value
		data, err := store.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, "6", string(data))
	})

	t.Run("CompareAndSwap", func(t *testing.T) {
		key := "test:cas:key"
		oldValue := []byte("old")
		newValue := []byte("new")

		// Set initial value
		err := store.Set(ctx, key, oldValue)
		assert.NoError(t, err)

		// CAS with correct old value
		err = store.CompareAndSwap(ctx, key, oldValue, newValue)
		assert.NoError(t, err)

		// Verify value changed
		got, err := store.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, newValue, got)

		// CAS with incorrect old value
		err = store.CompareAndSwap(ctx, key, oldValue, []byte("newer"))
		assert.Equal(t, ErrConflict, err)

		// Value should not have changed
		got, err = store.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, newValue, got)
	})

	t.Run("Batch Operations", func(t *testing.T) {
		prefix := "test:batch:"
		items := map[string][]byte{
			prefix + "key1": []byte("value1"),
			prefix + "key2": []byte("value2"),
			prefix + "key3": []byte("value3"),
		}

		// Batch set
		err := store.BatchSet(ctx, items)
		assert.NoError(t, err)

		// Batch get
		keys := []string{prefix + "key1", prefix + "key2", prefix + "key3", prefix + "missing"}
		result, err := store.BatchGet(ctx, keys)
		assert.NoError(t, err)
		assert.Len(t, result, 3)
		assert.Equal(t, items[prefix+"key1"], result[prefix+"key1"])
		assert.Equal(t, items[prefix+"key2"], result[prefix+"key2"])
		assert.Equal(t, items[prefix+"key3"], result[prefix+"key3"])
		assert.Nil(t, result[prefix+"missing"])

		// Batch delete
		deleteKeys := []string{prefix + "key1", prefix + "key3"}
		err = store.BatchDelete(ctx, deleteKeys)
		assert.NoError(t, err)

		// Verify deleted
		exists1, _ := store.Exists(ctx, prefix+"key1")
		exists2, _ := store.Exists(ctx, prefix+"key2")
		exists3, _ := store.Exists(ctx, prefix+"key3")
		assert.False(t, exists1)
		assert.True(t, exists2)
		assert.False(t, exists3)
	})

	t.Run("BatchSetWithTTL", func(t *testing.T) {
		prefix := "test:batchttl:"
		items := map[string][]byte{
			prefix + "key1": []byte("value1"),
			prefix + "key2": []byte("value2"),
		}

		// Batch set with TTL
		err := store.BatchSetWithTTL(ctx, items, 1*time.Second)
		assert.NoError(t, err)

		// Values should exist
		result, err := store.BatchGet(ctx, []string{prefix + "key1", prefix + "key2"})
		assert.NoError(t, err)
		assert.Len(t, result, 2)

		// Wait for expiration
		time.Sleep(2 * time.Second)

		// Values should be gone
		result, err = store.BatchGet(ctx, []string{prefix + "key1", prefix + "key2"})
		assert.NoError(t, err)
		assert.Len(t, result, 0)
	})

	t.Run("Transactions", func(t *testing.T) {
		prefix := "test:tx:"

		// Begin transaction
		tx, err := store.BeginTransaction(ctx)
		assert.NoError(t, err)

		// Queue operations
		err = tx.Set(prefix+"key1", []byte("value1"))
		assert.NoError(t, err)

		err = tx.SetWithTTL(prefix+"key2", []byte("value2"), 10*time.Second)
		assert.NoError(t, err)

		err = tx.Delete(prefix + "nonexistent")
		assert.NoError(t, err)

		// Commit transaction
		err = tx.Commit(ctx)
		assert.NoError(t, err)

		// Verify results
		val1, err := store.Get(ctx, prefix+"key1")
		assert.NoError(t, err)
		assert.Equal(t, []byte("value1"), val1)

		val2, err := store.Get(ctx, prefix+"key2")
		assert.NoError(t, err)
		assert.Equal(t, []byte("value2"), val2)
	})

	t.Run("Transaction Rollback", func(t *testing.T) {
		key := "test:tx:rollback"

		// Begin transaction
		tx, err := store.BeginTransaction(ctx)
		assert.NoError(t, err)

		// Queue operations
		err = tx.Set(key, []byte("should not be set"))
		assert.NoError(t, err)

		// Rollback
		err = tx.Rollback()
		assert.NoError(t, err)

		// Key should not exist
		exists, err := store.Exists(ctx, key)
		assert.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Scan Operations", func(t *testing.T) {
		prefix := "test:scan:"

		// Create test keys
		for i := 0; i < 10; i++ {
			key := prefix + string(rune('a'+i))
			err := store.Set(ctx, key, []byte(key))
			assert.NoError(t, err)
		}

		// Scan with pattern
		keys, err := store.Scan(ctx, prefix+"*", 5)
		assert.NoError(t, err)
		assert.LessOrEqual(t, len(keys), 5)
		for _, key := range keys {
			assert.Contains(t, key, prefix)
		}

		// Scan with prefix
		keys, err = store.ScanWithPrefix(ctx, prefix, 20)
		assert.NoError(t, err)
		assert.Equal(t, 10, len(keys))
	})

	t.Run("Health Check", func(t *testing.T) {
		err := store.Ping(ctx)
		assert.NoError(t, err)
	})
}

// TestValkeyPubSub tests the pub/sub functionality
func TestValkeyPubSub(t *testing.T) {
	// Skip if no Valkey URL is provided
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("Skipping Valkey pub/sub tests: TEST_VALKEY_URL not set")
	}

	config := ValkeyConfig{
		URL: valkeyURL,
		DB:  15,
	}

	store, err := OpenValkey(config)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Get pub/sub client
	vstore, ok := store.(*ValkeyStore)
	require.True(t, ok)
	pubsub := vstore.GetPubSub()

	t.Run("Subscribe and Publish", func(t *testing.T) {
		received := make(chan string, 1)
		pattern := "test:pubsub:*"

		// Subscribe
		err := pubsub.Subscribe(pattern, func(channel, message string) {
			received <- channel + ":" + message
		})
		assert.NoError(t, err)

		// Give subscription time to establish
		time.Sleep(100 * time.Millisecond)

		// Publish
		err = pubsub.Publish(ctx, "test:pubsub:channel1", "message1")
		assert.NoError(t, err)

		// Wait for message
		select {
		case msg := <-received:
			assert.Equal(t, "test:pubsub:channel1:message1", msg)
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for pubsub message")
		}
	})

	t.Run("Multiple Subscriptions", func(t *testing.T) {
		// Try to subscribe to same pattern again
		err := pubsub.Subscribe("test:pubsub:*", func(channel, message string) {})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already subscribed")
	})
}

// BenchmarkValkeyStore benchmarks Valkey operations
func BenchmarkValkeyStore(b *testing.B) {
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		b.Skip("Skipping Valkey benchmarks: TEST_VALKEY_URL not set")
	}

	config := ValkeyConfig{
		URL:          valkeyURL,
		DB:           15,
		MinIdleConns: 10,
	}

	store, err := OpenValkey(config)
	require.NoError(b, err)
	defer store.Close()

	ctx := context.Background()

	b.Run("Set", func(b *testing.B) {
		key := "bench:set"
		value := []byte("benchmark value")

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = store.Set(ctx, key, value)
		}
	})

	b.Run("Get", func(b *testing.B) {
		key := "bench:get"
		value := []byte("benchmark value")
		_ = store.Set(ctx, key, value)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = store.Get(ctx, key)
		}
	})

	b.Run("Increment", func(b *testing.B) {
		key := "bench:incr"

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = store.Increment(ctx, key, 1)
		}
	})
}
