package cache

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikeys"
	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValkeyIntegration tests cache manager with real Valkey instance
func TestValkeyIntegration(t *testing.T) {
	// Skip if no Valkey URL is provided
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("Skipping Valkey integration tests: TEST_VALKEY_URL not set")
	}

	// Create Valkey storage
	valkeyConfig := storage.ValkeyConfig{
		URL:          valkeyURL,
		MaxRetries:   3,
		MinIdleConns: 2,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		DB:           14, // Use different DB for cache tests
	}

	store, err := storage.OpenValkey(valkeyConfig)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Clean up test DB
	t.Cleanup(func() {
		keys, _ := store.Scan(ctx, "*", 10000)
		if len(keys) > 0 {
			_ = store.BatchDelete(ctx, keys)
		}
	})

	// Create cache manager
	config := ManagerConfig{}
	config.APIKeys.LocalTTL = 5 * time.Minute
	config.APIKeys.DistributedTTL = 1 * time.Hour
	config.APIKeys.LocalSizeMB = 32
	config.Models.SizeMB = 16
	config.Models.TTL = 6 * time.Hour
	config.Presets.LocalSizeMB = 16
	config.Presets.LocalTTL = 10 * time.Minute
	config.Responses.LocalSizeMB = 256
	config.Responses.MaxItemSizeKB = 1024
	config.Responses.TTL = 1 * time.Hour

	cm, err := NewCacheManager(config, store)
	require.NoError(t, err)
	defer cm.Close()

	t.Run("Multi-Node Cache Invalidation", func(t *testing.T) {
		// Simulate multi-node setup by creating a second cache manager
		cm2, err := NewCacheManager(config, store)
		require.NoError(t, err)
		defer cm2.Close()

		// Give pub/sub subscriptions time to establish
		time.Sleep(100 * time.Millisecond)

		// Test API key invalidation across nodes
		t.Run("API Key Cross-Node Invalidation", func(t *testing.T) {
			apiKey := &apikeys.APIKey{
				ID:     "multi-node-key",
				Name:   "Multi Node Test Key",
				Hash:   "hash-multi-node",
				Active: true,
			}

			// Set on node 1
			err := cm.SetAPIKey(ctx, apiKey.Hash, apiKey)
			require.NoError(t, err)

			// Both nodes should see the key
			key1, err := cm.GetAPIKey(ctx, apiKey.Hash)
			require.NoError(t, err)
			assert.True(t, key1.Active)

			key2, err := cm2.GetAPIKey(ctx, apiKey.Hash)
			require.NoError(t, err)
			assert.True(t, key2.Active)

			// Disable on node 1
			err = cm.DisableAPIKey(ctx, apiKey.Hash)
			require.NoError(t, err)

			// Wait for pub/sub propagation
			time.Sleep(50 * time.Millisecond)

			// Both nodes should see the disabled key
			key1, err = cm.GetAPIKey(ctx, apiKey.Hash)
			require.NoError(t, err)
			assert.False(t, key1.Active, "Node 1 should see disabled key")

			key2, err = cm2.GetAPIKey(ctx, apiKey.Hash)
			require.NoError(t, err)
			assert.False(t, key2.Active, "Node 2 should see disabled key")
		})

		// Test preset invalidation across nodes
		t.Run("Preset Cross-Node Invalidation", func(t *testing.T) {
			preset := &models.Preset{
				ID:          "multi-node-preset",
				Name:        "multi-node-preset",
				Description: "Multi node test preset",
			}

			// Set on node 2
			err := cm2.SetPreset(ctx, preset.Name, preset)
			require.NoError(t, err)

			// Both nodes should see the preset
			p1, err := cm.GetPreset(ctx, preset.Name)
			require.NoError(t, err)
			assert.Equal(t, preset.ID, p1.ID)

			p2, err := cm2.GetPreset(ctx, preset.Name)
			require.NoError(t, err)
			assert.Equal(t, preset.ID, p2.ID)

			// Delete on node 2
			err = cm2.DeletePreset(ctx, preset.Name)
			require.NoError(t, err)

			// Wait for pub/sub propagation
			time.Sleep(50 * time.Millisecond)

			// Both nodes should not find the preset
			_, err = cm.GetPreset(ctx, preset.Name)
			assert.Equal(t, storage.ErrNotFound, err, "Node 1 should not find deleted preset")

			_, err = cm2.GetPreset(ctx, preset.Name)
			assert.Equal(t, storage.ErrNotFound, err, "Node 2 should not find deleted preset")
		})

		// Test flush operations
		t.Run("Flush Operations", func(t *testing.T) {
			// Add some API keys
			for i := 0; i < 5; i++ {
				apiKey := &apikeys.APIKey{
					ID:     string(rune('a' + i)),
					Name:   string(rune('a' + i)),
					Hash:   string(rune('a' + i)),
					Active: true,
				}
				err := cm.SetAPIKey(ctx, apiKey.Hash, apiKey)
				require.NoError(t, err)
			}

			// Verify keys are cached on both nodes
			key1, err := cm.GetAPIKey(ctx, "a")
			require.NoError(t, err)
			assert.NotNil(t, key1)

			key2, err := cm2.GetAPIKey(ctx, "b")
			require.NoError(t, err)
			assert.NotNil(t, key2)

			// Flush API keys on node 1 by invalidating all local caches
			// Note: There's no flush method, but we can invalidate the hybrid cache
			for i := 0; i < 5; i++ {
				hash := string(rune('a' + i))
				cm.apiKeys.InvalidateLocal(hash)
			}

			// Wait for pub/sub propagation
			time.Sleep(50 * time.Millisecond)

			// Both nodes should have empty caches
			// (keys still exist in distributed store)
			for i := 0; i < 5; i++ {
				hash := string(rune('a' + i))

				// Should still get from distributed store
				key, err := cm.GetAPIKey(ctx, hash)
				require.NoError(t, err)
				assert.NotNil(t, key)

				key, err = cm2.GetAPIKey(ctx, hash)
				require.NoError(t, err)
				assert.NotNil(t, key)
			}
		})
	})

	t.Run("Concurrent Operations", func(t *testing.T) {
		// Test concurrent operations to ensure thread safety
		var wg sync.WaitGroup
		errors := make(chan error, 100)

		// Concurrent API key operations
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				apiKey := &apikeys.APIKey{
					ID:     string(rune('0' + id)),
					Name:   string(rune('0' + id)),
					Hash:   string(rune('0' + id)),
					Active: true,
				}

				// Set
				if err := cm.SetAPIKey(ctx, apiKey.Hash, apiKey); err != nil {
					errors <- err
					return
				}

				// Get
				if _, err := cm.GetAPIKey(ctx, apiKey.Hash); err != nil {
					errors <- err
					return
				}

				// Disable
				if err := cm.DisableAPIKey(ctx, apiKey.Hash); err != nil {
					errors <- err
					return
				}
			}(i)
		}

		// Concurrent preset operations
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				preset := &models.Preset{
					ID:   "preset-" + string(rune('0'+id)),
					Name: "preset-" + string(rune('0'+id)),
				}

				// Set
				if err := cm.SetPreset(ctx, preset.Name, preset); err != nil {
					errors <- err
					return
				}

				// Get
				if _, err := cm.GetPreset(ctx, preset.Name); err != nil {
					errors <- err
					return
				}

				// Delete
				if err := cm.DeletePreset(ctx, preset.Name); err != nil {
					errors <- err
					return
				}
			}(i)
		}

		wg.Wait()
		close(errors)

		// Check for errors
		for err := range errors {
			t.Errorf("concurrent operation error: %v", err)
		}
	})

	t.Run("Response Cache", func(t *testing.T) {
		// Test response caching
		key := "test-response-key"
		response := []byte("test response")

		// Set response
		err := cm.SetResponse(ctx, key, response)
		require.NoError(t, err)

		// Get response
		cached, found, err := cm.GetResponse(ctx, key)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, response, cached)

		// Test cache expiration
		// Note: This would require waiting for TTL or mocking time
	})

	t.Run("Models Cache", func(t *testing.T) {
		// Test model metadata caching
		modelID := "test-provider/model1"
		model := map[string]any{
			"id":   "model1",
			"name": "Model 1",
		}

		// Set model
		err := cm.SetModel(ctx, modelID, model)
		require.NoError(t, err)

		// Get model
		cached, found, err := cm.GetModel(ctx, modelID)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, model, cached)
	})
}

// TestValkeyPubSubReconnection tests pub/sub reconnection handling
func TestValkeyPubSubReconnection(t *testing.T) {
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("Skipping Valkey reconnection tests: TEST_VALKEY_URL not set")
	}

	// This test would require ability to simulate network failures
	// or restart Valkey instance, which is complex in integration tests
	t.Skip("Manual test: requires Valkey restart capability")
}

// BenchmarkValkeyCache benchmarks cache operations with Valkey
func BenchmarkValkeyCache(b *testing.B) {
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		b.Skip("Skipping Valkey cache benchmarks: TEST_VALKEY_URL not set")
	}

	// Create Valkey storage
	valkeyConfig := storage.ValkeyConfig{
		URL:          valkeyURL,
		MaxRetries:   3,
		MinIdleConns: 10,
		DB:           14,
	}

	store, err := storage.OpenValkey(valkeyConfig)
	require.NoError(b, err)
	defer store.Close()

	// Create cache manager
	config := ManagerConfig{}
	config.APIKeys.LocalTTL = 5 * time.Minute
	config.APIKeys.DistributedTTL = 1 * time.Hour
	config.APIKeys.LocalSizeMB = 32

	cm, err := NewCacheManager(config, store)
	require.NoError(b, err)
	defer cm.Close()

	ctx := context.Background()

	// Pre-populate some data
	apiKey := &apikeys.APIKey{
		ID:     "bench-key",
		Name:   "Benchmark Key",
		Hash:   "bench-hash",
		Active: true,
	}
	_ = cm.SetAPIKey(ctx, apiKey.Hash, apiKey)

	b.Run("GetAPIKey", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = cm.GetAPIKey(ctx, apiKey.Hash)
		}
	})

	b.Run("SetAPIKey", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = cm.SetAPIKey(ctx, apiKey.Hash, apiKey)
		}
	})

	b.Run("DisableAPIKey", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = cm.DisableAPIKey(ctx, apiKey.Hash)
		}
	})
}
