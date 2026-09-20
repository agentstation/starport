// Package cache owns optional local and shared byte caches.
package cache

import (
	"context"
	"time"
)

// Stats contains cache performance metrics.
type Stats struct {
	RetainedEntries int64  `json:"retained_entries,omitzero"`
	RetainedBytes   int64  `json:"retained_bytes,omitzero"`
	ActiveFills     int64  `json:"active_fills,omitzero"`
	DroppedFills    uint64 `json:"dropped_fills,omitzero"`
	FailedFills     uint64 `json:"failed_fills,omitzero"`
	CompletedFills  uint64 `json:"completed_fills,omitzero"`

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

// ResponseStore owns optional response bytes and their lifecycle.
// Blocking operations must honor context cancellation.
type ResponseStore interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Stats() Stats
	Close() error
}
