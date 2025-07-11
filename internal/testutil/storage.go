package testutil

import (
	"context"
	"testing"
	"time"
)

// StorageGetter is an interface for storage Get operations
type StorageGetter interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Exists(ctx context.Context, key string) (bool, error)
}

// WaitForExpiration waits for a key to expire in storage
func WaitForExpiration(t *testing.T, store StorageGetter, key string, timeout time.Duration) {
	t.Helper()

	ctx := context.Background()
	WaitFor(t, func() bool {
		_, err := store.Get(ctx, key)
		return err != nil // Key should return error when expired
	}, timeout, "key to expire")
}

// WaitForKeyNotExists waits for a key to not exist in storage
func WaitForKeyNotExists(t *testing.T, store StorageGetter, key string, timeout time.Duration) {
	t.Helper()

	ctx := context.Background()
	WaitFor(t, func() bool {
		exists, err := store.Exists(ctx, key)
		return err == nil && !exists
	}, timeout, "key to not exist")
}
