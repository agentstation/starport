package cache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLayeredCache(t *testing.T) {
	// Create mock storage
	mockKV := storage.NewMockStore()

	// Create cache with small size for testing
	config := Config{
		MaxSize:         100,
		MaxSizeInMB:     1,
		DefaultTTL:      1 * time.Hour,
		EnableMetrics:   true,
	}

	cache, err := New(config, mockKV)
	require.NoError(t, err)
	defer cache.Close()

	ctx := context.Background()

	t.Run("basic get/set operations", func(t *testing.T) {
		key := "test:key1"
		value := []byte("test value 1")

		// Cache miss
		val, found, err := cache.Get(ctx, key)
		assert.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, val)

		// Set value
		err = cache.Set(ctx, key, value, 1*time.Hour)
		assert.NoError(t, err)

		// Cache hit from local
		val, found, err = cache.Get(ctx, key)
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, value, val)
	})

	t.Run("TTL expiration", func(t *testing.T) {
		key := "test:ttl"
		value := []byte("expires soon")

		// Set with short TTL
		err := cache.Set(ctx, key, value, 100*time.Millisecond)
		assert.NoError(t, err)

		// Should exist immediately
		exists, err := cache.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)

		// Wait for expiration
		time.Sleep(150 * time.Millisecond)

		// Should not exist after TTL
		val, found, err := cache.Get(ctx, key)
		assert.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, val)
	})

	t.Run("delete operation", func(t *testing.T) {
		key := "test:delete"
		value := []byte("to be deleted")

		// Set value
		err := cache.Set(ctx, key, value, 1*time.Hour)
		assert.NoError(t, err)

		// Verify it exists
		exists, err := cache.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)

		// Delete
		err = cache.Delete(ctx, key)
		assert.NoError(t, err)

		// Verify deleted
		val, found, err := cache.Get(ctx, key)
		assert.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, val)
	})

	t.Run("batch operations", func(t *testing.T) {
		items := map[string][]byte{
			"batch:1": []byte("value1"),
			"batch:2": []byte("value2"),
			"batch:3": []byte("value3"),
		}

		// Set multiple
		err := cache.SetMulti(ctx, items, 1*time.Hour)
		assert.NoError(t, err)

		// Get multiple
		keys := []string{"batch:1", "batch:2", "batch:3", "batch:missing"}
		result, err := cache.GetMulti(ctx, keys)
		assert.NoError(t, err)
		assert.Len(t, result, 3)
		assert.Equal(t, items["batch:1"], result["batch:1"])
		assert.Equal(t, items["batch:2"], result["batch:2"])
		assert.Equal(t, items["batch:3"], result["batch:3"])
		assert.Nil(t, result["batch:missing"])
	})

	t.Run("invalidation with pattern", func(t *testing.T) {
		// Set multiple keys with pattern
		keys := map[string][]byte{
			"pattern:user:1": []byte("user1"),
			"pattern:user:2": []byte("user2"),
			"pattern:item:1": []byte("item1"),
		}

		for k, v := range keys {
			err := cache.Set(ctx, k, v, 1*time.Hour)
			assert.NoError(t, err)
		}

		// Invalidate user keys
		err := cache.Invalidate(ctx, "pattern:user:*")
		assert.NoError(t, err)

		// Check user keys are gone
		val, found, _ := cache.Get(ctx, "pattern:user:1")
		assert.False(t, found)
		assert.Nil(t, val)

		val, found, _ = cache.Get(ctx, "pattern:user:2")
		assert.False(t, found)
		assert.Nil(t, val)

		// Item key should still exist
		val, found, err = cache.Get(ctx, "pattern:item:1")
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, keys["pattern:item:1"], val)
	})

	t.Run("cache stats", func(t *testing.T) {
		// Reset stats by creating new cache
		cache2, err := New(config, mockKV)
		require.NoError(t, err)
		defer cache2.Close()

		// Generate some activity
		cache2.Get(ctx, "miss1") // miss
		cache2.Get(ctx, "miss2") // miss
		cache2.Set(ctx, "hit1", []byte("data"), 1*time.Hour)
		cache2.Get(ctx, "hit1") // hit
		cache2.Delete(ctx, "hit1")

		stats := cache2.Stats()
		assert.Equal(t, uint64(1), stats.Hits)
		assert.Equal(t, uint64(2), stats.Misses)
		assert.Equal(t, uint64(1), stats.Sets)
		assert.Equal(t, uint64(1), stats.Deletes)
		assert.InDelta(t, 0.333, stats.HitRate, 0.01)
	})

	t.Run("cache warming", func(t *testing.T) {
		// Pre-populate KV store
		warmKeys := []string{"warm:1", "warm:2", "warm:3"}
		for i, key := range warmKeys {
			mockKV.Set(ctx, key, []byte(fmt.Sprintf("warm-value-%d", i+1)))
		}

		// Create new cache and warm it
		warmCache, err := New(config, mockKV)
		require.NoError(t, err)
		defer warmCache.Close()

		err = warmCache.Warm(ctx, warmKeys)
		assert.NoError(t, err)

		// All keys should be in local cache (hit without KV access)
		for _, key := range warmKeys {
			val, found, err := warmCache.Get(ctx, key)
			assert.NoError(t, err)
			assert.True(t, found)
			assert.NotNil(t, val)
		}
	})
}
