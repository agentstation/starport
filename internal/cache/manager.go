package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/apikeys"
	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog/log"
)

// Manager manages different cache strategies for different data types
type Manager struct {
	// API Keys: Local + Distributed with pub/sub invalidation
	apiKeys *HybridCache

	// Rate Limits: Distributed only (needs consistency)
	rateLimits *DistributedCache

	// LLM Responses: Distributed only for multi-node, hybrid for single-node
	responses Cache

	// Model Metadata: Local only with long TTL
	models *LocalCache

	// Presets: Local + Distributed with pub/sub invalidation
	presets *HybridCache

	// Storage backend
	storage storage.KVStore

	// Pub/sub client for invalidation
	pubsub PubSubClient

	// Stop channel for graceful shutdown
	stopCh chan struct{}
	wg     sync.WaitGroup

	// Configuration
	config ManagerConfig

	// Key generator for cache keys
	keyGen *KeyGenerator
}

// ManagerConfig configures the cache manager
type ManagerConfig struct {
	// API Keys configuration
	APIKeys struct {
		LocalTTL       time.Duration `env:"LOCAL_TTL,default=5m"`
		DistributedTTL time.Duration `env:"DISTRIBUTED_TTL,default=1h"`
		LocalSizeMB    int64         `env:"LOCAL_SIZE_MB,default=32"`
	} `env:",prefix=API_KEYS_"`

	// Rate Limits configuration
	RateLimits struct {
		Strategy string `env:"STRATEGY,default=distributed"`
	} `env:",prefix=RATE_LIMITS_"`

	// LLM Responses configuration
	Responses struct {
		Strategy      string        `env:"STRATEGY,default=auto"`
		TTL           time.Duration `env:"TTL,default=1h"`
		MaxItemSizeKB int           `env:"MAX_ITEM_SIZE_KB,default=1024"`
		LocalSizeMB   int64         `env:"LOCAL_SIZE_MB,default=256"`
	} `env:",prefix=RESPONSES_"`

	// Model Metadata configuration
	Models struct {
		Strategy string        `env:"STRATEGY,default=local"`
		TTL      time.Duration `env:"TTL,default=6h"`
		SizeMB   int64         `env:"SIZE_MB,default=16"`
	} `env:",prefix=MODELS_"`

	// Presets configuration
	Presets struct {
		LocalTTL       time.Duration `env:"LOCAL_TTL,default=10m"`
		DistributedTTL time.Duration `env:"DISTRIBUTED_TTL,default=24h"`
		LocalSizeMB    int64         `env:"LOCAL_SIZE_MB,default=16"`
	} `env:",prefix=PRESETS_"`
}

// NewCacheManager creates a new cache manager with appropriate strategies for each data type
func NewCacheManager(config ManagerConfig, store storage.KVStore) (*Manager, error) {
	// Apply defaults if zero values
	if config.APIKeys.LocalSizeMB == 0 {
		config.APIKeys.LocalSizeMB = 32
	}
	if config.APIKeys.LocalTTL == 0 {
		config.APIKeys.LocalTTL = 5 * time.Minute
	}
	if config.APIKeys.DistributedTTL == 0 {
		config.APIKeys.DistributedTTL = 1 * time.Hour
	}
	if config.Models.SizeMB == 0 {
		config.Models.SizeMB = 16
	}
	if config.Models.TTL == 0 {
		config.Models.TTL = 6 * time.Hour
	}
	if config.Presets.LocalSizeMB == 0 {
		config.Presets.LocalSizeMB = 16
	}
	if config.Presets.LocalTTL == 0 {
		config.Presets.LocalTTL = 10 * time.Minute
	}
	if config.Presets.DistributedTTL == 0 {
		config.Presets.DistributedTTL = 24 * time.Hour
	}
	if config.Responses.LocalSizeMB == 0 {
		config.Responses.LocalSizeMB = 256
	}
	if config.Responses.TTL == 0 {
		config.Responses.TTL = 1 * time.Hour
	}
	if config.Responses.MaxItemSizeKB == 0 {
		config.Responses.MaxItemSizeKB = 1024
	}

	cm := &Manager{
		storage: store,
		stopCh:  make(chan struct{}),
		config:  config,
		keyGen:  NewKeyGenerator("starport"),
	}

	// Detect pub/sub capability
	if provider, ok := store.(PubSubProvider); ok {
		cm.pubsub = provider.GetPubSub()
		log.Info().Msg("pub/sub invalidation enabled")
	} else {
		cm.pubsub = &NoopPubSub{}
		log.Info().Msg("pub/sub not available, using TTL-based expiration")
	}

	// Initialize caches based on deployment mode
	isMultiNode := cm.pubsub != nil && !isNoopPubSub(cm.pubsub)

	// API Keys: Always hybrid with invalidation
	apiKeysConfig := HybridCacheConfig{
		LocalSizeMB:      config.APIKeys.LocalSizeMB,
		LocalTTL:         config.APIKeys.LocalTTL,
		Prefix:           storage.KeyPrefixAPIKey,
		InvalidatePrefix: ChannelAPIKeyInvalidate,
	}
	apiKeys, err := NewHybridCache(apiKeysConfig, store, cm.pubsub)
	if err != nil {
		return nil, fmt.Errorf("failed to create API keys cache: %w", err)
	}
	cm.apiKeys = apiKeys

	// Rate Limits: Always distributed for consistency
	cm.rateLimits = NewDistributedCache(store, storage.KeyPrefixRateLimit)

	// LLM Responses: Strategy depends on deployment mode
	if isMultiNode || config.Responses.Strategy == "distributed" {
		// Multi-node: distributed only
		cm.responses = NewDistributedCache(store, storage.KeyPrefixResponse)
		log.Info().Msg("using distributed cache for LLM responses")
	} else {
		// Single-node: can use hybrid for better performance
		respConfig := HybridCacheConfig{
			LocalSizeMB: config.Responses.LocalSizeMB,
			LocalTTL:    30 * time.Minute,
			Prefix:      storage.KeyPrefixResponse,
			// No invalidation for responses (immutable)
		}
		resp, err := NewHybridCache(respConfig, store, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create responses cache: %w", err)
		}
		cm.responses = resp
		log.Info().Msg("using hybrid cache for LLM responses")
	}

	// Model Metadata: Local only with long TTL
	models, err := NewLocalCache(config.Models.SizeMB, config.Models.TTL)
	if err != nil {
		return nil, fmt.Errorf("failed to create models cache: %w", err)
	}
	cm.models = models

	// Presets: Hybrid with invalidation
	presetsConfig := HybridCacheConfig{
		LocalSizeMB:      config.Presets.LocalSizeMB,
		LocalTTL:         config.Presets.LocalTTL,
		Prefix:           storage.KeyPrefixPreset,
		InvalidatePrefix: ChannelPresetInvalidate,
	}
	presets, err := NewHybridCache(presetsConfig, store, cm.pubsub)
	if err != nil {
		return nil, fmt.Errorf("failed to create presets cache: %w", err)
	}
	cm.presets = presets

	// Start invalidation listener if pub/sub is available
	if isMultiNode {
		cm.wg.Add(1)
		go cm.startInvalidationListener()
	}

	return cm, nil
}

// GetAPIKey retrieves an API key using hybrid cache
func (cm *Manager) GetAPIKey(ctx context.Context, hash string) (*apikeys.APIKey, error) {
	data, found, err := cm.apiKeys.Get(ctx, hash)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, storage.ErrNotFound
	}

	var apiKey apikeys.APIKey
	if err := json.Unmarshal(data, &apiKey); err != nil {
		return nil, fmt.Errorf("failed to unmarshal API key: %w", err)
	}

	return &apiKey, nil
}

// SetAPIKey stores an API key in hybrid cache
func (cm *Manager) SetAPIKey(ctx context.Context, hash string, apiKey *apikeys.APIKey) error {
	data, err := json.Marshal(apiKey)
	if err != nil {
		return fmt.Errorf("failed to marshal API key: %w", err)
	}

	return cm.apiKeys.Set(ctx, hash, data, cm.config.APIKeys.DistributedTTL)
}

// DisableAPIKey disables an API key and invalidates it across all nodes
func (cm *Manager) DisableAPIKey(ctx context.Context, hash string) error {
	// Get the current key
	apiKey, err := cm.GetAPIKey(ctx, hash)
	if err != nil {
		return err
	}

	// Disable it
	apiKey.Active = false

	// Update in storage
	if err := cm.SetAPIKey(ctx, hash, apiKey); err != nil {
		return err
	}

	// Invalidate local cache only
	cm.apiKeys.InvalidateLocal(hash)

	// Publish invalidation message if pub/sub is available
	if cm.pubsub != nil && !isNoopPubSub(cm.pubsub) {
		channel := ChannelAPIKeyInvalidate + hash
		if err := cm.pubsub.Publish(ctx, channel, ""); err != nil {
			log.Warn().Err(err).Str("channel", channel).Msg("failed to publish invalidation")
		}
	}

	return nil
}

// CheckRateLimit checks and updates rate limit (distributed only for consistency)
func (cm *Manager) CheckRateLimit(ctx context.Context, key string, limit int64, window time.Duration) (allowed bool, remaining int64, err error) {
	// Use storage directly for atomic increment
	fullKey := storage.KeyPrefixRateLimit + key
	count, err := cm.storage.Increment(ctx, fullKey, 1)
	if err != nil {
		return false, 0, err
	}

	// Set TTL on first increment
	if count == 1 {
		if err := cm.storage.ExpireAt(ctx, fullKey, time.Now().Add(window)); err != nil {
			log.Warn().Err(err).Msg("failed to set rate limit TTL")
		}
	}

	allowed = count <= limit
	remaining = limit - count
	if remaining < 0 {
		remaining = 0
	}

	return allowed, remaining, nil
}

// GetResponse retrieves a cached LLM response
func (cm *Manager) GetResponse(ctx context.Context, key string) ([]byte, bool, error) {
	return cm.responses.Get(ctx, key)
}

// SetResponse caches an LLM response
func (cm *Manager) SetResponse(ctx context.Context, key string, response []byte) error {
	// Check size limit
	if len(response) > cm.config.Responses.MaxItemSizeKB*1024 {
		log.Debug().
			Str("key", key).
			Int("size", len(response)).
			Msg("response too large to cache")
		return nil // Don't cache, but don't error
	}

	return cm.responses.Set(ctx, key, response, cm.config.Responses.TTL)
}

// GetModel retrieves model metadata (local cache only)
func (cm *Manager) GetModel(ctx context.Context, modelID string) (any, bool, error) {
	data, found, err := cm.models.Get(ctx, modelID)
	if err != nil || !found {
		return nil, found, err
	}

	// Unmarshal the data
	var model any
	if err := json.Unmarshal(data, &model); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal model: %w", err)
	}

	return model, true, nil
}

// SetModel caches model metadata (local cache only)
func (cm *Manager) SetModel(ctx context.Context, modelID string, model any) error {
	data, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("failed to marshal model: %w", err)
	}

	return cm.models.Set(ctx, modelID, data, cm.config.Models.TTL)
}

// InvalidateModels clears all model metadata from local cache
func (cm *Manager) InvalidateModels() {
	cm.models.Clear()
	log.Info().Msg("invalidated all model metadata")
}

// invalidateLocalModel invalidates a specific model from local cache
func (cm *Manager) invalidateLocalModel(key string) {
	_ = cm.models.Delete(context.Background(), key)
}

// GetPreset retrieves a preset using hybrid cache
func (cm *Manager) GetPreset(ctx context.Context, name string) (*models.Preset, error) {
	data, found, err := cm.presets.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, storage.ErrNotFound
	}

	var preset models.Preset
	if err := json.Unmarshal(data, &preset); err != nil {
		return nil, fmt.Errorf("failed to unmarshal preset: %w", err)
	}

	return &preset, nil
}

// SetPreset stores a preset in hybrid cache
func (cm *Manager) SetPreset(ctx context.Context, name string, preset *models.Preset) error {
	data, err := json.Marshal(preset)
	if err != nil {
		return fmt.Errorf("failed to marshal preset: %w", err)
	}

	return cm.presets.Set(ctx, name, data, cm.config.Presets.DistributedTTL)
}

// DeletePreset removes a preset and invalidates it across all nodes
func (cm *Manager) DeletePreset(ctx context.Context, name string) error {
	// Delete from distributed store
	fullKey := storage.KeyPrefixPreset + name
	if err := cm.storage.Delete(ctx, fullKey); err != nil && err != storage.ErrNotFound {
		return err
	}

	// Invalidate local cache
	cm.presets.InvalidateLocal(name)

	// Publish invalidation message if pub/sub is available
	if cm.pubsub != nil && !isNoopPubSub(cm.pubsub) {
		channel := ChannelPresetInvalidate + name
		if err := cm.pubsub.Publish(ctx, channel, ""); err != nil {
			log.Warn().Err(err).Str("channel", channel).Msg("failed to publish invalidation")
		}
	}

	return nil
}

// startInvalidationListener subscribes to invalidation channels
func (cm *Manager) startInvalidationListener() {
	defer cm.wg.Done()

	// Subscribe to invalidation patterns
	patterns := []string{
		ChannelAPIKeyInvalidate + "*",
		ChannelPresetInvalidate + "*",
		ChannelModelInvalidate + "*",
		ChannelAPIKeyFlush,
		ChannelPresetFlush,
		ChannelModelFlush,
	}

	for _, pattern := range patterns {
		if err := cm.pubsub.Subscribe(pattern, cm.handleInvalidation); err != nil {
			log.Error().
				Err(err).
				Str("pattern", pattern).
				Msg("failed to subscribe to invalidation channel")
		}
	}

	log.Info().Msg("cache invalidation listener started")

	// Wait for shutdown
	<-cm.stopCh
}

// handleInvalidation processes cache invalidation messages
func (cm *Manager) handleInvalidation(channel, message string) {
	log.Debug().
		Str("channel", channel).
		Str("message", message).
		Msg("received invalidation message")

	switch {
	case strings.HasPrefix(channel, ChannelAPIKeyInvalidate):
		key := strings.TrimPrefix(channel, ChannelAPIKeyInvalidate)
		cm.apiKeys.InvalidateLocal(key)

	case strings.HasPrefix(channel, ChannelPresetInvalidate):
		key := strings.TrimPrefix(channel, ChannelPresetInvalidate)
		cm.presets.InvalidateLocal(key)

	case strings.HasPrefix(channel, ChannelModelInvalidate):
		key := strings.TrimPrefix(channel, ChannelModelInvalidate)
		cm.invalidateLocalModel(key)

	case channel == ChannelAPIKeyFlush:
		cm.apiKeys.Clear()
		log.Info().Msg("flushed all API keys from local cache")

	case channel == ChannelPresetFlush:
		cm.presets.Clear()
		log.Info().Msg("flushed all presets from local cache")

	case channel == ChannelModelFlush:
		cm.models.Clear()
		log.Info().Msg("flushed all models from local cache")
	}
}

// GetKeyGenerator returns the key generator for creating cache keys
func (cm *Manager) GetKeyGenerator() *KeyGenerator {
	return cm.keyGen
}

// GetChatCompletion retrieves a cached chat completion response
func (cm *Manager) GetChatCompletion(ctx context.Context, key string) (*ChatCompletionResponse, error) {
	data, found, err := cm.responses.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil // Return nil, nil for cache miss
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal chat completion: %w", err)
	}

	return &resp, nil
}

// SetChatCompletion caches a chat completion response
func (cm *Manager) SetChatCompletion(ctx context.Context, key string, response *ChatCompletionResponse) error {
	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal chat completion: %w", err)
	}

	// Check size limit
	if len(data) > cm.config.Responses.MaxItemSizeKB*1024 {
		log.Debug().
			Str("key", key).
			Int("size", len(data)).
			Msg("chat completion too large to cache")
		return nil // Don't cache, but don't error
	}

	return cm.responses.Set(ctx, key, data, cm.config.Responses.TTL)
}

// GetEmbedding retrieves a cached embedding response
func (cm *Manager) GetEmbedding(ctx context.Context, key string) (*EmbeddingsResponse, error) {
	data, found, err := cm.responses.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil // Return nil, nil for cache miss
	}

	var resp EmbeddingsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal embeddings: %w", err)
	}

	return &resp, nil
}

// SetEmbedding caches an embedding response
func (cm *Manager) SetEmbedding(ctx context.Context, key string, response *EmbeddingsResponse) error {
	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal embeddings: %w", err)
	}

	// Check size limit
	if len(data) > cm.config.Responses.MaxItemSizeKB*1024 {
		log.Debug().
			Str("key", key).
			Int("size", len(data)).
			Msg("embeddings too large to cache")
		return nil // Don't cache, but don't error
	}

	return cm.responses.Set(ctx, key, data, cm.config.Responses.TTL)
}

// Stats returns aggregated cache statistics
func (cm *Manager) Stats() map[string]Stats {
	stats := make(map[string]Stats)

	// Get stats from each cache type
	stats["api_keys"] = cm.apiKeys.Stats()
	// Rate limits don't have local stats (distributed only)
	if hybrid, ok := cm.responses.(*HybridCache); ok {
		stats["responses"] = hybrid.Stats()
	}
	// Model metadata stats would come from LocalCache
	// Presets stats
	stats["presets"] = cm.presets.Stats()

	return stats
}

// Close gracefully shuts down the cache manager
func (cm *Manager) Close() error {
	// Signal shutdown
	close(cm.stopCh)

	// Wait for invalidation listener to stop
	cm.wg.Wait()

	// Close pub/sub client
	if cm.pubsub != nil {
		if err := cm.pubsub.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close pub/sub client")
		}
	}

	// Close individual caches
	if err := cm.apiKeys.Close(); err != nil {
		log.Warn().Err(err).Msg("failed to close API keys cache")
	}

	if hybrid, ok := cm.responses.(*HybridCache); ok {
		if err := hybrid.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close responses cache")
		}
	}

	if err := cm.models.Close(); err != nil {
		log.Warn().Err(err).Msg("failed to close models cache")
	}

	if err := cm.presets.Close(); err != nil {
		log.Warn().Err(err).Msg("failed to close presets cache")
	}

	return nil
}

// isNoopPubSub checks if the pub/sub client is a noop implementation
func isNoopPubSub(pubsub PubSubClient) bool {
	_, ok := pubsub.(*NoopPubSub)
	return ok
}
