package cache

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikeys"
	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCacheManagerWithPubSub tests cache invalidation with pub/sub
func TestCacheManagerWithPubSub(t *testing.T) {
	// Create mock storage
	store := storage.NewMockStore()

	// Create memory pub/sub
	pubsub := NewMemoryPubSub()

	// Create mock storage with pub/sub support
	mockStoreWithPubSub := &mockStoreWithPubSub{
		KVStore: store,
		pubsub:  pubsub,
	}

	// Create cache manager with proper config
	config := ManagerConfig{}
	config.APIKeys.LocalTTL = 5 * time.Minute
	config.APIKeys.DistributedTTL = 1 * time.Hour
	config.APIKeys.LocalSizeMB = 32
	config.Models.SizeMB = 16
	config.Models.TTL = 6 * time.Hour
	config.Presets.LocalSizeMB = 16
	config.Presets.LocalTTL = 10 * time.Minute
	config.Responses.LocalSizeMB = 256
	config.Responses.MaxItemSizeKB = 1024 // 1MB
	config.Responses.TTL = 1 * time.Hour

	cm, err := NewCacheManager(config, mockStoreWithPubSub)
	require.NoError(t, err)
	defer cm.Close()

	ctx := context.Background()

	// Test API key invalidation
	t.Run("API key invalidation", func(t *testing.T) {
		// Create an API key
		apiKey := &apikeys.APIKey{
			ID:     "test-key",
			Name:   "Test Key",
			Hash:   "hash123",
			Active: true,
		}

		// Set the API key
		err := cm.SetAPIKey(ctx, apiKey.Hash, apiKey)
		require.NoError(t, err)

		// Verify it's cached
		cached, err := cm.GetAPIKey(ctx, apiKey.Hash)
		require.NoError(t, err)
		assert.Equal(t, apiKey.ID, cached.ID)
		assert.True(t, cached.Active)

		// Disable the API key (should trigger invalidation)
		err = cm.DisableAPIKey(ctx, apiKey.Hash)
		require.NoError(t, err)

		// Give pub/sub time to propagate
		time.Sleep(10 * time.Millisecond)

		// Get the key again - should fetch from distributed store
		cached, err = cm.GetAPIKey(ctx, apiKey.Hash)
		require.NoError(t, err)
		assert.False(t, cached.Active, "API key should be disabled")
	})

	// Test preset invalidation
	t.Run("Preset invalidation", func(t *testing.T) {
		// Create a preset
		preset := &models.Preset{
			ID:          "test-preset",
			Name:        "Test Preset",
			Description: "Test preset description",
		}

		// Set the preset
		err := cm.SetPreset(ctx, preset.Name, preset)
		require.NoError(t, err)

		// Verify it's cached
		cached, err := cm.GetPreset(ctx, preset.Name)
		require.NoError(t, err)
		assert.Equal(t, preset.ID, cached.ID)

		// Delete the preset (should trigger invalidation)
		err = cm.DeletePreset(ctx, preset.Name)
		require.NoError(t, err)

		// Give pub/sub time to propagate
		time.Sleep(10 * time.Millisecond)

		// Try to get the preset again - should not be found
		_, err = cm.GetPreset(ctx, preset.Name)
		assert.Equal(t, storage.ErrNotFound, err)
	})

	// Test rate limits (distributed only, no invalidation)
	t.Run("Rate limits consistency", func(t *testing.T) {
		key := "user:123"
		limit := int64(10)
		window := 1 * time.Minute

		// Check rate limit multiple times
		for i := 0; i < 5; i++ {
			allowed, remaining, err := cm.CheckRateLimit(ctx, key, limit, window)
			require.NoError(t, err)
			assert.True(t, allowed)
			assert.Equal(t, limit-int64(i+1), remaining)
		}

		// Verify it's consistent (no local cache)
		allowed, remaining, err := cm.CheckRateLimit(ctx, key, limit, window)
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, int64(4), remaining) // 10 - 6
	})

	// Test model metadata (local only)
	t.Run("Model metadata local cache", func(t *testing.T) {
		modelID := "gpt-4"
		modelData := map[string]interface{}{
			"id":             modelID,
			"context_length": 8192,
		}

		// Set model metadata
		err := cm.SetModel(ctx, modelID, modelData)
		require.NoError(t, err)

		// Get model metadata (should be from local cache)
		cached, found, err := cm.GetModel(ctx, modelID)
		require.NoError(t, err)
		assert.True(t, found)

		// Verify the data
		cachedMap, ok := cached.(map[string]interface{})
		require.True(t, ok, "cached type: %T", cached)
		assert.Equal(t, modelID, cachedMap["id"])

		// Invalidate all models
		cm.InvalidateModels()

		// Should not be found after invalidation
		_, found, err = cm.GetModel(ctx, modelID)
		require.NoError(t, err)
		assert.False(t, found)
	})
}

// TestCacheManagerMultiNode simulates multi-node cache behavior
func TestCacheManagerMultiNode(t *testing.T) {
	// Shared storage and pub/sub
	sharedStore := storage.NewMockStore()
	sharedPubSub := NewMemoryPubSub()

	// Create two cache managers (simulating two nodes)
	mockStore1 := &mockStoreWithPubSub{
		KVStore: sharedStore,
		pubsub:  sharedPubSub,
	}
	mockStore2 := &mockStoreWithPubSub{
		KVStore: sharedStore,
		pubsub:  sharedPubSub,
	}

	config := ManagerConfig{}
	config.APIKeys.LocalTTL = 5 * time.Minute
	config.APIKeys.LocalSizeMB = 32
	config.Models.SizeMB = 16
	config.Models.TTL = 6 * time.Hour
	config.Presets.LocalSizeMB = 16
	config.Presets.LocalTTL = 10 * time.Minute
	config.Responses.LocalSizeMB = 256
	config.Responses.MaxItemSizeKB = 1024
	config.APIKeys.DistributedTTL = 1 * time.Hour
	config.Presets.DistributedTTL = 24 * time.Hour

	cm1, err := NewCacheManager(config, mockStore1)
	require.NoError(t, err)
	defer cm1.Close()

	cm2, err := NewCacheManager(config, mockStore2)
	require.NoError(t, err)
	defer cm2.Close()

	ctx := context.Background()

	// Node 1 sets an API key
	apiKey := &apikeys.APIKey{
		ID:     "shared-key",
		Name:   "Shared Key",
		Hash:   "shared-hash",
		Active: true,
	}

	err = cm1.SetAPIKey(ctx, apiKey.Hash, apiKey)
	require.NoError(t, err)

	// Node 2 should be able to get it
	cached, err := cm2.GetAPIKey(ctx, apiKey.Hash)
	require.NoError(t, err)
	assert.Equal(t, apiKey.ID, cached.ID)
	assert.True(t, cached.Active)

	// Node 1 disables the key
	err = cm1.DisableAPIKey(ctx, apiKey.Hash)
	require.NoError(t, err)

	// Give pub/sub time to propagate
	time.Sleep(20 * time.Millisecond)

	// Node 2 should see it as disabled (after invalidation)
	cached, err = cm2.GetAPIKey(ctx, apiKey.Hash)
	require.NoError(t, err)
	assert.False(t, cached.Active, "API key should be disabled on node 2")
}

// TestCacheManagerSingleNode tests single-node behavior (no pub/sub)
func TestCacheManagerSingleNode(t *testing.T) {
	// Create mock storage without pub/sub
	store := storage.NewMockStore()

	config := ManagerConfig{}
	config.Responses.Strategy = "auto"
	config.Responses.LocalSizeMB = 256
	config.Responses.MaxItemSizeKB = 1024
	config.Responses.TTL = 1 * time.Hour
	config.APIKeys.LocalSizeMB = 32
	config.APIKeys.LocalTTL = 5 * time.Minute
	config.APIKeys.DistributedTTL = 1 * time.Hour
	config.Models.SizeMB = 16
	config.Models.TTL = 6 * time.Hour
	config.Presets.LocalSizeMB = 16
	config.Presets.LocalTTL = 10 * time.Minute
	config.Presets.DistributedTTL = 24 * time.Hour

	cm, err := NewCacheManager(config, store)
	require.NoError(t, err)
	defer cm.Close()

	ctx := context.Background()

	// Test response caching (should use hybrid cache in single-node)
	t.Run("Response caching single-node", func(t *testing.T) {
		key := "response:123"
		data := []byte(`{"response": "test"}`)

		// Set response
		err := cm.SetResponse(ctx, key, data)
		require.NoError(t, err)

		// Get response (should hit local cache)
		cached, found, err := cm.GetResponse(ctx, key)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, data, cached)
	})
}

// mockStoreWithPubSub implements both KVStore and PubSubProvider
type mockStoreWithPubSub struct {
	storage.KVStore
	pubsub PubSubClient
}

func (m *mockStoreWithPubSub) GetPubSub() PubSubClient {
	return m.pubsub
}

// TestCacheStats tests cache statistics aggregation
func TestCacheStats(t *testing.T) {
	store := storage.NewMockStore()
	pubsub := NewMemoryPubSub()
	mockStore := &mockStoreWithPubSub{
		KVStore: store,
		pubsub:  pubsub,
	}

	config := ManagerConfig{}
	config.APIKeys.LocalSizeMB = 32
	config.APIKeys.LocalTTL = 5 * time.Minute
	config.APIKeys.DistributedTTL = 1 * time.Hour
	config.Models.SizeMB = 16
	config.Models.TTL = 6 * time.Hour
	config.Presets.LocalSizeMB = 16
	config.Presets.LocalTTL = 10 * time.Minute
	config.Presets.DistributedTTL = 24 * time.Hour
	config.Responses.LocalSizeMB = 256
	config.Responses.MaxItemSizeKB = 1024
	cm, err := NewCacheManager(config, mockStore)
	require.NoError(t, err)
	defer cm.Close()

	ctx := context.Background()

	// Perform some cache operations
	apiKey := &apikeys.APIKey{ID: "test", Hash: "test-hash", Active: true}
	err = cm.SetAPIKey(ctx, apiKey.Hash, apiKey)
	require.NoError(t, err)

	// Get it a few times to generate hits
	for i := 0; i < 3; i++ {
		_, err := cm.GetAPIKey(ctx, apiKey.Hash)
		require.NoError(t, err)
	}

	// Get stats
	stats := cm.Stats()
	assert.NotNil(t, stats)
	assert.Contains(t, stats, "api_keys")
	assert.Contains(t, stats, "presets")
}

// TestConfigValidation tests configuration validation
func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  ManagerConfig
		wantErr bool
	}{
		{
			name:    "default config",
			config:  ManagerConfig{},
			wantErr: false,
		},
		{
			name: "custom config",
			config: ManagerConfig{
				APIKeys: struct {
					LocalTTL       time.Duration `env:"LOCAL_TTL,default=5m"`
					DistributedTTL time.Duration `env:"DISTRIBUTED_TTL,default=1h"`
					LocalSizeMB    int64         `env:"LOCAL_SIZE_MB,default=32"`
				}{
					LocalTTL:    10 * time.Minute,
					LocalSizeMB: 64,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := storage.NewMockStore()
			_, err := NewCacheManager(tt.config, store)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// BenchmarkCacheManager benchmarks cache manager operations
func BenchmarkCacheManager(b *testing.B) {
	store := storage.NewMockStore()
	config := ManagerConfig{}
	config.APIKeys.LocalSizeMB = 32
	config.Models.SizeMB = 16
	config.Presets.LocalSizeMB = 16
	config.Responses.LocalSizeMB = 256
	cm, _ := NewCacheManager(config, store)
	defer cm.Close()

	ctx := context.Background()

	// Prepare test data
	apiKey := &apikeys.APIKey{
		ID:     "bench-key",
		Hash:   "bench-hash",
		Active: true,
	}
	_ = cm.SetAPIKey(ctx, apiKey.Hash, apiKey)

	b.Run("GetAPIKey", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = cm.GetAPIKey(ctx, apiKey.Hash)
		}
	})

	b.Run("CheckRateLimit", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _, _ = cm.CheckRateLimit(ctx, "bench-user", 100, time.Minute)
		}
	})
}
