package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog/log"
)

// Manager manages different cache strategies for different data types
type Manager struct {
	// LLM Responses: Distributed only for multi-node, hybrid for single-node
	responses Cache

	// Model Metadata: Local only with long TTL
	models *LocalCache

	// Configuration
	config ManagerConfig
}

// ManagerConfig configures the cache manager
type ManagerConfig struct {
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
}

// NewCacheManager creates a new cache manager with appropriate strategies for each data type
func NewCacheManager(config ManagerConfig, store storage.KVStore) (*Manager, error) {
	// Apply defaults if zero values
	if config.Models.SizeMB == 0 {
		config.Models.SizeMB = 16
	}
	if config.Models.TTL == 0 {
		config.Models.TTL = 6 * time.Hour
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
		config: config,
	}

	// Detect pub/sub capability
	var pubsub PubSubClient = &NoopPubSub{}
	if provider, ok := store.(PubSubProvider); ok {
		pubsub = provider.GetPubSub()
		log.Info().Msg("pub/sub invalidation enabled")
	} else {
		log.Info().Msg("pub/sub not available, using TTL-based expiration")
	}

	// Initialize caches based on deployment mode
	isMultiNode := pubsub != nil && !isNoopPubSub(pubsub)

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

	return cm, nil
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

// Stats returns aggregated cache statistics.
func (cm *Manager) Stats() map[string]Stats {
	stats := make(map[string]Stats)
	if hybrid, ok := cm.responses.(*HybridCache); ok {
		stats["responses"] = hybrid.Stats()
	}

	return stats
}

// Close gracefully shuts down the cache manager
func (cm *Manager) Close() error {
	if hybrid, ok := cm.responses.(*HybridCache); ok {
		if err := hybrid.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close responses cache")
		}
	}

	if err := cm.models.Close(); err != nil {
		log.Warn().Err(err).Msg("failed to close models cache")
	}

	return nil
}

// isNoopPubSub checks if the pub/sub client is a noop implementation
func isNoopPubSub(pubsub PubSubClient) bool {
	_, ok := pubsub.(*NoopPubSub)
	return ok
}
