package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoRequestWithRetry_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return server error to trigger retry
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	config := ProviderConfig{
		MaxRetries:        3,
		RetryDelay:        10 * time.Millisecond,
		BackoffMultiplier: 2.0,
	}

	// Create a context that we'll cancel during retry
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)

	// Cancel context after a short delay
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := doRequestWithRetry(client, req, config)
	if err != ErrContextCanceled {
		t.Errorf("Expected ErrContextCanceled, got %v", err)
	}
}

func TestDoRequestWithRetry_NetworkError(t *testing.T) {
	// Use an invalid URL to simulate network error
	client := &http.Client{Timeout: 1 * time.Second}
	config := ProviderConfig{
		MaxRetries:        2,
		RetryDelay:        10 * time.Millisecond,
		BackoffMultiplier: 2.0,
	}

	req, _ := http.NewRequest("GET", "http://invalid.test.domain.that.does.not.exist:99999", nil)

	_, err := doRequestWithRetry(client, req, config)
	if err == nil {
		t.Error("Expected error for network failure")
	}
}
