package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testStorageGetter interface {
	Get(context.Context, string) ([]byte, error)
	Exists(context.Context, string) (bool, error)
}

func waitForExpiration(
	t *testing.T,
	store testStorageGetter,
	key string,
	timeout time.Duration,
) {
	t.Helper()
	waitFor(t, func() bool {
		_, err := store.Get(t.Context(), key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatalf("get key while waiting for expiration: %v", err)
		}
		return errors.Is(err, ErrNotFound)
	}, timeout, "key to expire")
}

func waitForKeyNotExists(
	t *testing.T,
	store testStorageGetter,
	key string,
	timeout time.Duration,
) {
	t.Helper()
	waitFor(t, func() bool {
		exists, err := store.Exists(t.Context(), key)
		if err != nil {
			t.Fatalf("check key while waiting for deletion: %v", err)
		}
		return !exists
	}, timeout, "key to not exist")
}

func waitFor(
	t *testing.T,
	condition func() bool,
	timeout time.Duration,
	message string,
) {
	t.Helper()
	if condition() {
		return
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ticker.C:
			if condition() {
				return
			}
		case <-timer.C:
			t.Fatalf("timeout waiting for condition: %s", message)
		}
	}
}
