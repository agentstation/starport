package storage

import (
	"context"
	"time"
)

// LifetimeReader reads a value and its remaining lifetime from one record version.
// Zero lifetime means no finite expiry evidence. Callers must anchor deadlines before the read.
// maxBytes bounds the returned payload before copying or transfer.
type LifetimeReader interface {
	ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error)
}
