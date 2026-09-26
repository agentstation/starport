package availability

import (
	"context"
	"testing"
	"time"
)

type blockedAdvisoryStore struct {
	entered chan string
	release chan struct{}
}

func (s *blockedAdvisoryStore) wait(ctx context.Context, operation string) error {
	select {
	case s.entered <- operation:
	default:
	}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *blockedAdvisoryStore) SetWithTTL(ctx context.Context, _ string, _ []byte, _ time.Duration) error {
	return s.wait(ctx, "publish")
}

func (s *blockedAdvisoryStore) ScanWithPrefix(ctx context.Context, _ string, _ int) ([]string, error) {
	return nil, s.wait(ctx, "scan")
}

func (s *blockedAdvisoryStore) BatchGet(ctx context.Context, _ []string) (map[string][]byte, error) {
	return nil, s.wait(ctx, "read")
}

func assertAdvisoryCallbackLocal(t *testing.T, store *blockedAdvisoryStore, callback func()) {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() {
		close(store.release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("callback did not stop after storage release")
		}
	})
	go func() { defer close(done); callback() }()
	select {
	case operation := <-store.entered:
		t.Fatalf("inference callback reached remote advisory storage: %s", operation)
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("local inference callback did not finish")
	}
}

func TestAdvisoryHealthCallbacksAvoidStorage(t *testing.T) {
	for _, name := range []string{"refresh", "failure"} {
		t.Run(name, func(t *testing.T) {
			store := &blockedAdvisoryStore{entered: make(chan string, 1), release: make(chan struct{})}
			tracker := sharedTracker(t, nil, store, "callback-test")
			assertAdvisoryCallbackLocal(t, store, func() {
				switch name {
				case "refresh":
					tracker.Refresh(t.Context())
				case "failure":
					tracker.RecordFailure(sharedTestRoute(), offeringFailure(), time.Second)
				}
			})
		})
	}
}
