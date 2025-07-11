// Package testutil provides common test utilities for the Starport project
package testutil

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// WaitFor polls a condition function until it returns true or the timeout is reached.
// It's useful for replacing time.Sleep in tests with a more deterministic approach.
func WaitFor(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout waiting for condition: %s", message)
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}

// WaitForServer waits for a server to be ready by checking a health endpoint
func WaitForServer(t *testing.T, url string, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	WaitFor(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK
	}, timeout, "server to be ready")
}

// WaitForChannel waits for a value to be received on a channel
func WaitForChannel[T any](t *testing.T, ch <-chan T, timeout time.Duration) T {
	t.Helper()

	select {
	case val := <-ch:
		return val
	case <-time.After(timeout):
		var zero T
		t.Fatalf("timeout waiting for channel value")
		return zero
	}
}

// Eventually runs a function repeatedly until it returns true or the timeout is reached
// This is similar to WaitFor but returns a boolean instead of failing the test
func Eventually(condition func() bool, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if condition() {
				return true
			}
		}
	}
}

// AssertEventually asserts that a condition becomes true within the timeout
func AssertEventually(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	t.Helper()

	if !Eventually(condition, timeout) {
		t.Errorf("condition not met within %v: %s", timeout, message)
	}
}
