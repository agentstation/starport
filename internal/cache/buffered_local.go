package cache

import (
	"context"
	"sync"
	"time"
)

// BufferedLocalCache owns local bytes and a bounded optional fill queue.
type BufferedLocalCache struct {
	local *LocalCache
	fills *fillQueue
	once  sync.Once
}

// NewBufferedLocalCache starts workers owned by the returned cache.
func NewBufferedLocalCache(sizeMB int64, ttl time.Duration) (*BufferedLocalCache, error) {
	local, err := NewLocalCache(sizeMB, ttl)
	if err != nil {
		return nil, err
	}
	return &BufferedLocalCache{local: local, fills: newFillQueue(local)}, nil
}

// Get reads local bytes without waiting for outstanding fills.
func (c *BufferedLocalCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return c.local.Get(ctx, key)
}

// Set admits an optional fill; success does not guarantee a subsequent hit.
func (c *BufferedLocalCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.fills.enqueue(ctx, key, value, ttl)
}

// FillStatus reports bounds and pressure without keys or payloads.
func (c *BufferedLocalCache) FillStatus() FillStatus {
	if c == nil {
		return FillStatus{}
	}
	return fillStatus(c.fills.stats())
}

// Close joins workers before releasing their local store.
func (c *BufferedLocalCache) Close() error {
	c.once.Do(func() { c.fills.close(); _ = c.local.Close() })
	return nil
}
