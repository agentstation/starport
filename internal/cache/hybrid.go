package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/rs/zerolog/log"
)

// HybridCache implements a two-layer cache with local (Ristretto) and distributed (KV store) layers
// It supports pub/sub based invalidation for multi-node consistency
type HybridCache struct {
	local        *ristretto.Cache[string, []byte]
	distributed  storage.KVStore
	pubsub       PubSubClient
	prefix       string
	localTTL     time.Duration
	invalidateCh string // Channel prefix for invalidation
}

// HybridCacheConfig configures a hybrid cache
type HybridCacheConfig struct {
	LocalSizeMB      int64
	LocalTTL         time.Duration
	Prefix           string
	InvalidatePrefix string
}

// NewHybridCache creates a new hybrid cache
func NewHybridCache(config HybridCacheConfig, store storage.KVStore, pubsub PubSubClient) (*HybridCache, error) {
	// Configure Ristretto
	ristrettoConfig := &ristretto.Config[string, []byte]{
		NumCounters: config.LocalSizeMB * 10 * 1024, // 10x the cache size
		MaxCost:     config.LocalSizeMB * 1024 * 1024,
		BufferItems: 64,
		Metrics:     true,
	}

	local, err := ristretto.NewCache[string, []byte](ristrettoConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create local cache: %w", err)
	}

	return &HybridCache{
		local:        local,
		distributed:  store,
		pubsub:       pubsub,
		prefix:       config.Prefix,
		localTTL:     config.LocalTTL,
		invalidateCh: config.InvalidatePrefix,
	}, nil
}

// Get retrieves a value from the cache
func (h *HybridCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	// 1. Check local cache first
	if value, found := h.local.Get(key); found {
		log.Debug().
			Str("key", key).
			Str("cache", "local").
			Msg("cache hit")
		return value, true, nil
	}

	// 2. Check distributed cache
	fullKey := h.prefix + key
	value, err := h.distributed.Get(ctx, fullKey)
	if err != nil {
		if err == storage.ErrNotFound {
			log.Debug().
				Str("key", key).
				Str("cache", "distributed").
				Msg("cache miss")
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to get from distributed cache: %w", err)
	}

	// 3. Populate local cache
	h.local.SetWithTTL(key, value, int64(len(value)), h.localTTL)
	h.local.Wait()

	log.Debug().
		Str("key", key).
		Str("cache", "distributed").
		Msg("cache hit")

	return value, true, nil
}

// Set stores a value in both cache layers
func (h *HybridCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	fullKey := h.prefix + key

	// 1. Set in distributed store first (source of truth)
	if err := h.distributed.SetWithTTL(ctx, fullKey, value, ttl); err != nil {
		return fmt.Errorf("failed to set in distributed cache: %w", err)
	}

	// 2. Set in local cache
	localTTL := h.localTTL
	if ttl > 0 && ttl < localTTL {
		localTTL = ttl // Use shorter TTL if specified
	}
	h.local.SetWithTTL(key, value, int64(len(value)), localTTL)
	h.local.Wait()

	log.Debug().
		Str("key", key).
		Dur("ttl", ttl).
		Msg("cached value")

	return nil
}

// Delete removes a value from both cache layers and publishes invalidation
func (h *HybridCache) Delete(ctx context.Context, key string) error {
	fullKey := h.prefix + key

	// 1. Delete from distributed cache
	if err := h.distributed.Delete(ctx, fullKey); err != nil && err != storage.ErrNotFound {
		return fmt.Errorf("failed to delete from distributed cache: %w", err)
	}

	// 2. Delete from local cache
	h.local.Del(key)

	// 3. Publish invalidation if pub/sub is available
	if h.pubsub != nil && h.invalidateCh != "" {
		channel := h.invalidateCh + key
		if err := h.pubsub.Publish(ctx, channel, ""); err != nil {
			log.Warn().
				Err(err).
				Str("channel", channel).
				Msg("failed to publish invalidation")
		} else {
			log.Debug().
				Str("channel", channel).
				Msg("published invalidation")
		}
	}

	return nil
}

// Invalidate removes all items matching the pattern from both caches
func (h *HybridCache) Invalidate(_ context.Context, _ string) error {
	// For hybrid cache, we just clear local cache on pattern
	// The distributed cache invalidation happens through pub/sub
	h.local.Clear()
	return nil
}

// InvalidateLocal removes a key from local cache only (used by invalidation handler)
func (h *HybridCache) InvalidateLocal(key string) {
	h.local.Del(key)
	log.Debug().
		Str("key", key).
		Msg("invalidated local cache")
}

// Clear removes all items from local cache
func (h *HybridCache) Clear() {
	h.local.Clear()
	log.Debug().Msg("cleared local cache")
}

// Stats returns local cache statistics
func (h *HybridCache) Stats() Stats {
	metrics := h.local.Metrics

	hits := metrics.Hits()
	misses := metrics.Misses()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return Stats{
		Hits:    hits,
		Misses:  misses,
		HitRate: hitRate,
		Size:    int64(metrics.KeysAdded()) - int64(metrics.KeysEvicted()), // #nosec G115 -- Metrics values are safe
	}
}

// Exists checks if a key exists in the cache
func (h *HybridCache) Exists(ctx context.Context, key string) (bool, error) {
	// Check local cache first
	if _, found := h.local.Get(key); found {
		return true, nil
	}

	// Check distributed cache
	fullKey := h.prefix + key
	return h.distributed.Exists(ctx, fullKey)
}

// GetMulti retrieves multiple values from the cache
func (h *HybridCache) GetMulti(ctx context.Context, keys []string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	missingKeys := []string{}

	// Check local cache first
	for _, key := range keys {
		if value, found := h.local.Get(key); found {
			result[key] = value
		} else {
			missingKeys = append(missingKeys, key)
		}
	}

	// Get missing keys from distributed
	if len(missingKeys) > 0 {
		fullKeys := make([]string, len(missingKeys))
		for i, key := range missingKeys {
			fullKeys[i] = h.prefix + key
		}

		kvValues, err := h.distributed.BatchGet(ctx, fullKeys)
		if err != nil {
			return result, fmt.Errorf("failed to batch get from distributed: %w", err)
		}

		// Add to result and populate local cache
		for i, key := range missingKeys {
			fullKey := fullKeys[i]
			if value, ok := kvValues[fullKey]; ok {
				result[key] = value
				h.local.SetWithTTL(key, value, int64(len(value)), h.localTTL)
			}
		}
		// Wait for all values to be set
		h.local.Wait()
	}

	return result, nil
}

// SetMulti stores multiple values in the cache
func (h *HybridCache) SetMulti(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	// Prepare items for distributed store
	distItems := make(map[string][]byte)
	for key, value := range items {
		distItems[h.prefix+key] = value
	}

	// Set in distributed store
	if err := h.distributed.BatchSetWithTTL(ctx, distItems, ttl); err != nil {
		return fmt.Errorf("failed to batch set in distributed: %w", err)
	}

	// Set in local cache
	localTTL := h.localTTL
	if ttl > 0 && ttl < localTTL {
		localTTL = ttl
	}

	for key, value := range items {
		h.local.SetWithTTL(key, value, int64(len(value)), localTTL)
	}
	h.local.Wait()

	return nil
}

// Warm pre-loads the cache with frequently accessed data
func (h *HybridCache) Warm(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	// Get from distributed
	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = h.prefix + key
	}

	values, err := h.distributed.BatchGet(ctx, fullKeys)
	if err != nil {
		return fmt.Errorf("failed to warm cache: %w", err)
	}

	// Load into local cache
	for i, key := range keys {
		fullKey := fullKeys[i]
		if value, ok := values[fullKey]; ok {
			h.local.SetWithTTL(key, value, int64(len(value)), h.localTTL)
		}
	}
	// Wait for all values to be set
	h.local.Wait()

	return nil
}

// Close closes the local cache
func (h *HybridCache) Close() error {
	h.local.Close()
	return nil
}

// DistributedCache implements a cache using only the distributed KV store
type DistributedCache struct {
	store  storage.KVStore
	prefix string
}

// NewDistributedCache creates a new distributed-only cache
func NewDistributedCache(store storage.KVStore, prefix string) *DistributedCache {
	return &DistributedCache{
		store:  store,
		prefix: prefix,
	}
}

// Get retrieves a value from the distributed cache
func (d *DistributedCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	fullKey := d.prefix + key
	value, err := d.store.Get(ctx, fullKey)
	if err != nil {
		if err == storage.ErrNotFound {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to get from store: %w", err)
	}
	return value, true, nil
}

// Set stores a value in the distributed cache
func (d *DistributedCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	fullKey := d.prefix + key
	if ttl > 0 {
		return d.store.SetWithTTL(ctx, fullKey, value, ttl)
	}
	return d.store.Set(ctx, fullKey, value)
}

// Delete removes a value from the distributed cache
func (d *DistributedCache) Delete(ctx context.Context, key string) error {
	fullKey := d.prefix + key
	return d.store.Delete(ctx, fullKey)
}

// Exists checks if a key exists in the distributed cache
func (d *DistributedCache) Exists(ctx context.Context, key string) (bool, error) {
	fullKey := d.prefix + key
	return d.store.Exists(ctx, fullKey)
}

// GetMulti retrieves multiple values from the distributed cache
func (d *DistributedCache) GetMulti(ctx context.Context, keys []string) (map[string][]byte, error) {
	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = d.prefix + key
	}

	kvValues, err := d.store.BatchGet(ctx, fullKeys)
	if err != nil {
		return nil, fmt.Errorf("failed to batch get: %w", err)
	}

	// Transform back to original keys
	result := make(map[string][]byte)
	for i, key := range keys {
		fullKey := fullKeys[i]
		if value, ok := kvValues[fullKey]; ok {
			result[key] = value
		}
	}

	return result, nil
}

// SetMulti stores multiple values in the distributed cache
func (d *DistributedCache) SetMulti(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	// Transform keys
	fullItems := make(map[string][]byte)
	for key, value := range items {
		fullItems[d.prefix+key] = value
	}

	if ttl > 0 {
		return d.store.BatchSetWithTTL(ctx, fullItems, ttl)
	}
	return d.store.BatchSet(ctx, fullItems)
}

// Invalidate removes all items matching the pattern
func (d *DistributedCache) Invalidate(ctx context.Context, pattern string) error {
	// Use prefix scan to find matching keys
	keys, err := d.store.ScanWithPrefix(ctx, d.prefix+pattern, 1000)
	if err != nil {
		return fmt.Errorf("failed to scan keys: %w", err)
	}

	if len(keys) > 0 {
		return d.store.BatchDelete(ctx, keys)
	}

	return nil
}

// Stats returns empty stats for distributed cache
func (d *DistributedCache) Stats() Stats {
	// Distributed cache doesn't track local stats
	return Stats{}
}

// Warm pre-loads data (no-op for distributed cache)
func (d *DistributedCache) Warm(_ context.Context, _ []string) error {
	// No warming needed for distributed cache
	return nil
}

// Close is a no-op for distributed cache
func (d *DistributedCache) Close() error {
	// Nothing to close
	return nil
}

// LocalCache implements a local-only cache using Ristretto
type LocalCache struct {
	cache *ristretto.Cache[string, []byte]
	ttl   time.Duration
}

// NewLocalCache creates a new local-only cache
func NewLocalCache(sizeMB int64, ttl time.Duration) (*LocalCache, error) {
	config := &ristretto.Config[string, []byte]{
		NumCounters: sizeMB * 10 * 1024,
		MaxCost:     sizeMB * 1024 * 1024,
		BufferItems: 64,
		Metrics:     true,
	}

	cache, err := ristretto.NewCache[string, []byte](config)
	if err != nil {
		return nil, fmt.Errorf("failed to create local cache: %w", err)
	}

	return &LocalCache{
		cache: cache,
		ttl:   ttl,
	}, nil
}

// Get retrieves a value from the local cache
func (l *LocalCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	value, found := l.cache.Get(key)
	return value, found, nil
}

// Set stores a value in the local cache
func (l *LocalCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl == 0 {
		ttl = l.ttl
	}
	l.cache.SetWithTTL(key, value, int64(len(value)), ttl)
	// Wait for value to be set in Ristretto
	l.cache.Wait()
	return nil
}

// Delete removes a value from the local cache
func (l *LocalCache) Delete(_ context.Context, key string) error {
	l.cache.Del(key)
	return nil
}

// Clear removes all items from the cache
func (l *LocalCache) Clear() {
	l.cache.Clear()
}

// Exists checks if a key exists in the local cache
func (l *LocalCache) Exists(_ context.Context, key string) (bool, error) {
	_, found := l.cache.Get(key)
	return found, nil
}

// GetMulti retrieves multiple values from the local cache
func (l *LocalCache) GetMulti(_ context.Context, keys []string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for _, key := range keys {
		if value, found := l.cache.Get(key); found {
			result[key] = value
		}
	}
	return result, nil
}

// SetMulti stores multiple values in the local cache
func (l *LocalCache) SetMulti(_ context.Context, items map[string][]byte, ttl time.Duration) error {
	if ttl == 0 {
		ttl = l.ttl
	}
	for key, value := range items {
		l.cache.SetWithTTL(key, value, int64(len(value)), ttl)
	}
	l.cache.Wait()
	return nil
}

// Invalidate removes all items matching the pattern
func (l *LocalCache) Invalidate(_ context.Context, _ string) error {
	// Simple pattern matching for local cache
	// This is a simplified implementation - in production you might want more sophisticated pattern matching
	l.cache.Clear() // For now, just clear all
	return nil
}

// Stats returns cache statistics
func (l *LocalCache) Stats() Stats {
	metrics := l.cache.Metrics
	hits := metrics.Hits()
	misses := metrics.Misses()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return Stats{
		Hits:    hits,
		Misses:  misses,
		HitRate: hitRate,
		Size:    int64(metrics.KeysAdded()) - int64(metrics.KeysEvicted()), // #nosec G115 -- Metrics values are safe
	}
}

// Warm pre-loads the cache (no-op for local cache)
func (l *LocalCache) Warm(_ context.Context, _ []string) error {
	// Local cache doesn't support warming from external source
	return nil
}

// Close closes the cache
func (l *LocalCache) Close() error {
	l.cache.Close()
	return nil
}
