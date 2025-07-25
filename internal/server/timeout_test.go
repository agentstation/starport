package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/registry"
)

func TestRequestTimeout(t *testing.T) {
	// Create a test handler that takes longer than the timeout
	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("should not reach here"))
		case <-r.Context().Done():
			// Context was cancelled due to timeout
			return
		}
	})

	// Create router with timeout middleware
	router := chi.NewRouter()
	router.Use(middleware.Timeout(500 * time.Millisecond))
	router.Get("/slow", slowHandler)

	// Make request
	req := httptest.NewRequest("GET", "/slow", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	router.ServeHTTP(w, req)
	elapsed := time.Since(start)

	// Should timeout after ~500ms
	assert.Less(t, elapsed, 1*time.Second)
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
}

func TestConfigurableRequestTimeout(t *testing.T) {
	// Create a custom handler that simulates a slow endpoint
	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			return
		}
	})

	// Create server with custom timeout
	config := &Config{
		Port:           0,
		RequestTimeout: 100 * time.Millisecond,
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST"},
			AllowedHeaders: []string{"Content-Type"},
		},
	}

	// Test the timeout middleware directly
	router := chi.NewRouter()
	router.Use(Timeout(config.RequestTimeout))
	router.Get("/test-slow", slowHandler)

	// Make request
	req := httptest.NewRequest("GET", "/test-slow", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	router.ServeHTTP(w, req)
	elapsed := time.Since(start)

	// Should timeout after ~100ms
	assert.Less(t, elapsed, 150*time.Millisecond)
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
}

func TestShutdownTimeout(t *testing.T) {
	config := &Config{
		Port:            0,
		ShutdownTimeout: 100 * time.Millisecond,
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST"},
			AllowedHeaders: []string{"Content-Type"},
		},
	}

	reg := registry.NewEmpty()
	server := New(config, reg)

	// Start server
	go func() {
		_ = server.Start()
	}()

	// Wait for server to be ready
	// Note: Since we can't check server readiness directly here, we keep a minimal delay
	// This is acceptable as we're testing shutdown behavior, not startup
	time.Sleep(10 * time.Millisecond)

	// Create a context that will be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := server.Shutdown(ctx)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 150*time.Millisecond)
}

func TestContextPropagation(t *testing.T) {
	// Test that context cancellation propagates through the request chain
	config := &Config{
		Port:           0,
		RequestTimeout: 5 * time.Second,
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST"},
			AllowedHeaders: []string{"Content-Type"},
		},
	}

	reg := registry.NewEmpty()
	server := New(config, reg)

	// Track if context was cancelled using a channel
	contextCancelled := make(chan bool, 1)

	server.router.Get("/context-test", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			contextCancelled <- true
			return
		case <-time.After(100 * time.Millisecond):
			contextCancelled <- false
			w.WriteHeader(http.StatusOK)
		}
	})

	// Create request with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/context-test", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	// Cancel context after a short delay
	cancelDone := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
		close(cancelDone)
	}()

	// Serve the request
	go func() {
		server.router.ServeHTTP(w, req)
	}()

	// Wait for cancellation and verify
	<-cancelDone
	select {
	case cancelled := <-contextCancelled:
		assert.True(t, cancelled, "context cancellation should propagate to handler")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for context cancellation")
	}
}
