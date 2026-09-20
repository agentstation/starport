package cache

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

// readBacking returns a deadline only when the store proves the value's lifetime.
func readBacking(ctx context.Context, store storage.KVStore, key string, maxBytes int) ([]byte, time.Time, error) {
	started := time.Now()
	reader, ok := store.(storage.LifetimeReader)
	if !ok {
		value, err := store.GetBounded(ctx, key, maxBytes)
		return value, time.Time{}, err
	}
	value, lifetime, err := reader.ReadWithLifetime(ctx, key, maxBytes)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	if lifetime <= 0 {
		return value, time.Time{}, nil
	}
	deadline := started.Add(lifetime)
	if !time.Now().Before(deadline) {
		return nil, time.Time{}, storage.ErrNotFound
	}
	return value, deadline, nil
}

// expiringValue keeps the source deadline independent of cache queue timing.
type expiringValue struct {
	data     []byte
	deadline time.Time
}

func (v expiringValue) valid() bool { return !v.deadline.IsZero() && time.Now().Before(v.deadline) }
