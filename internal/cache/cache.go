package cache

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/rs/zerolog/log"
)

// layeredCache implements the Cache interface with multi-layer caching
type layeredCache struct {
	config   Config
	local    *ristretto.Cache[string, expiringValue]
	kv       storage.KVStore
	policies map[PolicyType]Policy
	stats    cacheStats
}

// cacheStats tracks cache performance metrics
type cacheStats struct {
	hits      atomic.Uint64
	misses    atomic.Uint64
	sets      atomic.Uint64
	deletes   atomic.Uint64
	evictions atomic.Uint64
}

// New creates a new layered cache instance
func New(config Config, kv storage.KVStore) (Cache, error) {
	// Configure Ristretto with appropriate settings
	ristrettoConfig := &ristretto.Config[string, expiringValue]{
		NumCounters: config.MaxSize * 10, // 10x the cache size for better accuracy
		MaxCost:     config.MaxSizeInMB * 1024 * 1024,
		BufferItems: 64,
		OnEvict: func(item *ristretto.Item[expiringValue]) {
			log.Debug().
				Int64("cost", item.Cost).
				Msg("item evicted from cache")
		},
		Metrics: config.EnableMetrics,
	}

	localCache, err := ristretto.NewCache[string, expiringValue](ristrettoConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create ristretto cache: %w", err)
	}

	lc := &layeredCache{
		config:   config,
		local:    localCache,
		kv:       kv,
		policies: DefaultPolicies(),
	}

	// Warm up cache if configured
	if len(config.WarmupKeys) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := lc.Warm(ctx, config.WarmupKeys); err != nil {
			log.Warn().Err(err).Msg("failed to warm cache")
		}
	}

	return lc, nil
}

// Get retrieves a value from the cache
func (lc *layeredCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	// Check local cache first
	if value, found := lc.local.Get(key); found && value.valid() {
		lc.stats.hits.Add(1)
		return value.data, true, nil
	}

	// Fall back to KV store
	value, deadline, err := readBacking(ctx, lc.kv, key, int(lc.config.MaxSizeInMB*1024*1024))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			lc.stats.misses.Add(1)
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to get from kv store: %w", err)
	}

	// Populate local cache
	if ttl := time.Until(deadline); !deadline.IsZero() && ttl > 0 {
		lc.local.SetWithTTL(key, expiringValue{data: value, deadline: deadline}, int64(len(value)), ttl)
	}

	lc.stats.hits.Add(1)
	return value, true, nil
}

// Set stores a value in the cache with a TTL
func (lc *layeredCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	lc.stats.sets.Add(1)

	// Use default TTL if not specified
	if ttl == 0 {
		ttl = lc.config.DefaultTTL
	}

	// Store in KV store with TTL
	if err := lc.kv.SetWithTTL(ctx, key, value, ttl); err != nil {
		return fmt.Errorf("failed to set in kv store: %w", err)
	}

	// A later read must prove the stored version's actual expiry.
	lc.local.Del(key)
	lc.local.Wait()

	return nil
}

// Delete removes a value from the cache
func (lc *layeredCache) Delete(ctx context.Context, key string) error {
	lc.stats.deletes.Add(1)

	// Delete from local cache
	lc.local.Del(key)
	lc.local.Wait()

	// Delete from KV store
	if err := lc.kv.Delete(ctx, key); err != nil && err != storage.ErrNotFound {
		return fmt.Errorf("failed to delete from kv store: %w", err)
	}

	return nil
}

// Exists checks if a key exists in the cache
func (lc *layeredCache) Exists(ctx context.Context, key string) (bool, error) {
	_, found, err := lc.Get(ctx, key)
	return found, err
}

// GetMulti retrieves multiple values from the cache
func (lc *layeredCache) GetMulti(ctx context.Context, keys []string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for _, key := range keys {
		value, found, err := lc.Get(ctx, key)
		if err != nil {
			return result, err
		}
		if found {
			result[key] = value
		}
	}
	return result, nil
}

// SetMulti stores multiple values in the cache with a TTL
func (lc *layeredCache) SetMulti(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	lc.stats.sets.Add(uint64(len(items)))

	// Use default TTL if not specified
	if ttl == 0 {
		ttl = lc.config.DefaultTTL
	}

	// Store in KV store
	if err := lc.kv.BatchSetWithTTL(ctx, items, ttl); err != nil {
		return fmt.Errorf("failed to batch set in kv store: %w", err)
	}

	for key := range items {
		lc.local.Del(key)
	}
	lc.local.Wait()

	return nil
}

// Invalidate removes all items matching the pattern
func (lc *layeredCache) Invalidate(ctx context.Context, pattern string) error {
	// Convert pattern to regex-like pattern for matching
	// Support * as wildcard
	matchPattern := strings.ReplaceAll(pattern, "*", "")

	// Scan and delete from KV store
	keys, err := lc.kv.ScanWithPrefix(ctx, matchPattern, 1000)
	if err != nil {
		return fmt.Errorf("failed to scan keys: %w", err)
	}

	for len(keys) > 0 {
		// Delete batch from KV store
		if err := lc.kv.BatchDelete(ctx, keys); err != nil {
			return fmt.Errorf("failed to batch delete: %w", err)
		}

		// Delete from local cache
		for _, key := range keys {
			lc.local.Del(key)
		}
		lc.local.Wait()

		lc.stats.deletes.Add(uint64(len(keys)))

		// Get next batch
		keys, err = lc.kv.ScanWithPrefix(ctx, matchPattern, 1000)
		if err != nil {
			return fmt.Errorf("failed to scan keys: %w", err)
		}
	}

	return nil
}

// Stats returns cache statistics
func (lc *layeredCache) Stats() Stats {
	hits := lc.stats.hits.Load()
	misses := lc.stats.misses.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	metrics := lc.local.Metrics

	// Calculate size safely
	keysAdded := metrics.KeysAdded()
	keysEvicted := metrics.KeysEvicted()
	size := int64(0)
	if keysAdded > keysEvicted {
		diff := keysAdded - keysEvicted
		if diff <= math.MaxInt64 { // Check if fits in int64
			size = int64(diff) //nolint:gosec // bounds checked above
		}
	}

	// Calculate size in bytes safely
	costAdded := metrics.CostAdded()
	costEvicted := metrics.CostEvicted()
	sizeInBytes := int64(0)
	if costAdded > costEvicted {
		diff := costAdded - costEvicted
		if diff <= math.MaxInt64 { // Check if fits in int64
			sizeInBytes = int64(diff) //nolint:gosec // bounds checked above
		}
	}

	return Stats{
		Hits:        hits,
		Misses:      misses,
		Sets:        lc.stats.sets.Load(),
		Deletes:     lc.stats.deletes.Load(),
		Evictions:   lc.stats.evictions.Load(),
		HitRate:     hitRate,
		Size:        size,
		SizeInBytes: sizeInBytes,
	}
}

// Warm pre-loads the cache with frequently accessed data
func (lc *layeredCache) Warm(ctx context.Context, keys []string) error {
	_, err := lc.GetMulti(ctx, keys)
	return err
}

// Close gracefully shuts down the cache
func (lc *layeredCache) Close() error {
	lc.local.Close()
	return nil
}
