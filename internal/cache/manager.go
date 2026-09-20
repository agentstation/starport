package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Manager manages different cache strategies for different data types
type Manager struct {
	// Responses use a cache-owned store.
	responses      ResponseStore
	localResponses bool
	fills          *fillQueue
	closeOnce      sync.Once
	closeErr       error

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

// NewCacheManager uses bounded memory unless the caller supplies a cache-only store.
// The manager owns the supplied store after successful construction.
func NewCacheManager(config ManagerConfig, store ResponseStore) (*Manager, error) {
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
		config:         config,
		localResponses: store == nil,
	}

	switch config.Responses.Strategy {
	case "", "auto", "local":
		if store != nil {
			return nil, errors.New("shared response cache requires distributed strategy")
		}
	case "distributed":
		if store == nil {
			return nil, errors.New("distributed response cache requires a cache-only store")
		}
	default:
		return nil, errors.New("unknown response cache strategy")
	}

	// Model Metadata: Local only with long TTL
	models, err := NewLocalCache(config.Models.SizeMB, config.Models.TTL)
	if err != nil {
		return nil, fmt.Errorf("failed to create models cache: %w", err)
	}
	cm.models = models
	if store == nil {
		responses, err := NewLocalCache(config.Responses.LocalSizeMB, config.Responses.TTL)
		if err != nil {
			_ = models.Close()
			return nil, fmt.Errorf("create response cache: %w", err)
		}
		store = responses
	}
	cm.responses = store
	cm.fills = newFillQueue(store)

	return cm, nil
}

// GetResponse retrieves a cached LLM response
func (cm *Manager) GetResponse(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !cm.localResponses {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, remoteReadTimeout)
		defer cancel()
	}
	value, found, err := cm.responses.Get(ctx, key)
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	return value, found, err
}

// SetResponse admits an optional fill without waiting for persistence.
func (cm *Manager) SetResponse(ctx context.Context, key string, response []byte) error {
	// Check size limit
	if len(response) > cm.config.Responses.MaxItemSizeKB*1024 {
		log.Debug().
			Str("key", key).
			Int("size", len(response)).
			Msg("response too large to cache")
		cm.fills.dropped.Add(1)
		return nil // Oversized responses bypass the cache.
	}

	return cm.fills.enqueue(ctx, key, response, cm.config.Responses.TTL)
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
	return map[string]Stats{"responses": cm.responses.Stats(), "models": cm.models.Stats(), "response_fills": cm.fills.stats()}
}

// Close releases both cache stores.
func (cm *Manager) Close() error {
	cm.closeOnce.Do(func() {
		cm.fills.close()
		cm.closeErr = errors.Join(cm.responses.Close(), cm.models.Close())
	})
	return cm.closeErr
}
