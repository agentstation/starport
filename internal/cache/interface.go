// Package cache provides a multi-layer caching system for LLM responses
// with in-memory and persistent storage backends.
package cache

import (
	"context"
	"time"
)

// Cache defines the interface for the caching system.
// It provides a multi-layer cache with in-memory (hot) and persistent (cold) storage.
type Cache interface {
	// Get retrieves a value from the cache.
	// Returns the value and a boolean indicating if it was found.
	Get(ctx context.Context, key string) ([]byte, bool, error)

	// Set stores a value in the cache with a TTL.
	// A zero TTL means the item never expires.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes a value from the cache.
	Delete(ctx context.Context, key string) error

	// Exists checks if a key exists in the cache.
	Exists(ctx context.Context, key string) (bool, error)

	// GetMulti retrieves multiple values from the cache.
	// Returns a map of key to value for found items.
	GetMulti(ctx context.Context, keys []string) (map[string][]byte, error)

	// SetMulti stores multiple values in the cache with a TTL.
	SetMulti(ctx context.Context, items map[string][]byte, ttl time.Duration) error

	// Invalidate removes all items matching the pattern.
	// Pattern supports wildcards: * matches any sequence of characters.
	Invalidate(ctx context.Context, pattern string) error

	// Stats returns cache statistics.
	Stats() Stats

	// Warm pre-loads the cache with frequently accessed data.
	Warm(ctx context.Context, keys []string) error

	// Close gracefully shuts down the cache.
	Close() error
}

// Stats contains cache performance metrics.
type Stats struct {
	// Hits is the number of cache hits
	Hits uint64 `json:"hits"`
	// Misses is the number of cache misses
	Misses uint64 `json:"misses"`
	// Sets is the number of items added to cache
	Sets uint64 `json:"sets"`
	// Deletes is the number of items deleted from cache
	Deletes uint64 `json:"deletes"`
	// Evictions is the number of items evicted due to size/TTL
	Evictions uint64 `json:"evictions"`
	// HitRate is the cache hit rate (hits / (hits + misses))
	HitRate float64 `json:"hit_rate"`
	// Size is the current number of items in cache
	Size int64 `json:"size"`
	// SizeInBytes is the approximate memory usage
	SizeInBytes int64 `json:"size_in_bytes"`
}

// Config represents cache configuration
type Config struct {
	// MaxSize is the maximum number of items in the in-memory cache
	MaxSize int64 `env:"MAX_SIZE,default=10000"`
	// MaxSizeInMB is the maximum memory usage in MB for the in-memory cache
	MaxSizeInMB int64 `env:"MAX_SIZE_MB,default=256"`
	// DefaultTTL is the default TTL for cached items
	DefaultTTL time.Duration `env:"DEFAULT_TTL,default=1h"`
	// CleanupInterval is how often to run the cleanup process
	CleanupInterval time.Duration `env:"CLEANUP_INTERVAL,default=10m"`
	// EnableMetrics enables detailed metrics collection
	EnableMetrics bool `env:"ENABLE_METRICS,default=true"`
	// WarmupKeys is a list of keys to pre-load on startup
	WarmupKeys []string `env:"WARMUP_KEYS"`
}

// Policy defines caching policies for different types of data
type Policy struct {
	// TTL is the time-to-live for this type of data
	TTL time.Duration
	// MaxSize is the maximum size in bytes for cacheable items
	MaxSize int64
	// Compress indicates whether to compress the data
	Compress bool
	// SkipCache indicates whether to skip caching entirely
	SkipCache bool
}

// PolicyType represents different types of cacheable data
type PolicyType string

const (
	// PolicyTypeChatCompletion is for chat completion responses
	PolicyTypeChatCompletion PolicyType = "chat_completion"
	// PolicyTypeEmbedding is for embedding responses
	PolicyTypeEmbedding PolicyType = "embedding"
	// PolicyTypeModel is for model list responses
	PolicyTypeModel PolicyType = "model"
	// PolicyTypeProvider is for provider metadata
	PolicyTypeProvider PolicyType = "provider"
)

// DefaultPolicies returns the default caching policies
func DefaultPolicies() map[PolicyType]Policy {
	return map[PolicyType]Policy{
		PolicyTypeChatCompletion: {
			TTL:       1 * time.Hour,
			MaxSize:   1024 * 1024, // 1MB
			Compress:  true,
			SkipCache: false,
		},
		PolicyTypeEmbedding: {
			TTL:       24 * time.Hour,
			MaxSize:   512 * 1024, // 512KB
			Compress:  true,
			SkipCache: false,
		},
		PolicyTypeModel: {
			TTL:       1 * time.Hour,
			MaxSize:   256 * 1024, // 256KB
			Compress:  false,
			SkipCache: false,
		},
		PolicyTypeProvider: {
			TTL:       6 * time.Hour,
			MaxSize:   128 * 1024, // 128KB
			Compress:  false,
			SkipCache: false,
		},
	}
}