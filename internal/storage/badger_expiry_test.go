package storage

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestBadgerExpiryPreservesConcurrentIncrements(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	t.Cleanup(cleanup)
	const key = "concurrent-expiry-counter"
	const increments = 1000
	if err := store.Set(t.Context(), key, SerializeInt64(0)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Hour)
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Go(func() {
		<-start
		for range increments {
			if _, err := store.Increment(t.Context(), key, 1); err != nil {
				t.Errorf("increment: %v", err)
				return
			}
			runtime.Gosched()
		}
	})
	workers.Go(func() {
		<-start
		for range increments {
			if err := store.ExpireAt(t.Context(), key, deadline); err != nil {
				t.Errorf("set expiry: %v", err)
				return
			}
			runtime.Gosched()
		}
	})
	close(start)
	workers.Wait()
	data, err := store.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	value, err := DeserializeInt64(data)
	if err != nil {
		t.Fatal(err)
	}
	if value != increments {
		t.Fatalf("expiry lost increments: got %d, want %d", value, increments)
	}
	ttl, err := store.GetTTL(t.Context(), key)
	if err != nil || ttl <= 0 || ttl > time.Until(deadline)+time.Second {
		t.Fatalf("expiry was not retained: ttl=%v, error=%v", ttl, err)
	}
}

func TestBadgerExpiryCancellationPreservesValueAndDeadline(t *testing.T) {
	store, cleanup := createTestBadgerStore(t)
	t.Cleanup(cleanup)
	const key = "cancelled-expiry"
	if err := store.Set(t.Context(), key, []byte("retained")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.ExpireAt(ctx, key, time.Now().Add(-time.Second)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled expiry: %v", err)
	}
	value, err := store.Get(t.Context(), key)
	if err != nil || string(value) != "retained" {
		t.Fatalf("cancelled expiry changed value: %q, %v", value, err)
	}
	ttl, err := store.GetTTL(t.Context(), key)
	if err != nil || ttl != 0 {
		t.Fatalf("cancelled expiry changed deadline: %v, %v", ttl, err)
	}
}
