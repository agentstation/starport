package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

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
	l.cache.SetWithTTL(key, value, int64(cap(value)), ttl)
	// Wait for Ristretto to apply the value.
	l.cache.Wait()
	return nil
}

// Clear removes all items from the cache
func (l *LocalCache) Clear() {
	l.cache.Clear()
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

// Close closes the cache
func (l *LocalCache) Close() error {
	l.cache.Close()
	return nil
}
