package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

func TestNew(t *testing.T) {
	config := &Config{
		Port:            8080,
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    10 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 30 * time.Second,
		CORS: CORSConfig{
			AllowedOrigins:   []string{"*"},
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
			ExposedHeaders:   []string{},
			AllowCredentials: true,
			MaxAge:           300,
		},
	}

	server := newTestServer(t, config)
	if server == nil {
		t.Fatal("expected server to be created")
	}

	if server.cfg != config {
		t.Error("expected server config to match")
	}

	if server.router == nil {
		t.Error("expected router to be initialized")
	}

	if server.httpServer == nil {
		t.Error("expected HTTP server to be initialized")
	}
}

func TestNewRequiresReadyDependencies(t *testing.T) {
	config := &Config{Port: 8080}
	ready := newTestServer(t, config)
	base := Dependencies{
		Service: ready.service, Identities: ready.identities, Accounts: ready.accounts,
		ProviderKeys: ready.providerKeys, RateLimits: ready.rateLimits,
		ProviderOperations: ready.providerOperations,
	}
	tests := []struct {
		name   string
		mutate func(*Dependencies)
		cause  error
	}{
		{"service", func(value *Dependencies) { value.Service = nil }, ErrServiceRequired},
		{"identities", func(value *Dependencies) { value.Identities = nil }, ErrIdentitiesRequired},
		{"accounts", func(value *Dependencies) { value.Accounts = nil }, ErrAccountsRequired},
		{"provider keys", func(value *Dependencies) { value.ProviderKeys = nil }, ErrProviderKeysRequired},
		{"rate limits", func(value *Dependencies) { value.RateLimits = nil }, ErrRateLimitsRequired},
		{"provider operations", func(value *Dependencies) { value.ProviderOperations = nil }, ErrProviderOperationsRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := base
			test.mutate(&dependencies)
			_, err := New(config, dependencies)
			if !errors.Is(err, test.cause) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
	config := &Config{
		Port: 8080,
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST"},
		},
	}

	server := newTestServer(t, config)

	// Test that middleware is properly configured
	// Use /health/live which doesn't require authentication
	req := httptest.NewRequest("GET", "/health/live", nil)
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// CORS headers are only set when Origin header is present in request
	// Let's test with Origin header
	req2 := httptest.NewRequest("GET", "/health/live", nil)
	req2.Header.Set("Origin", "http://localhost:3000")
	w2 := httptest.NewRecorder()

	server.router.ServeHTTP(w2, req2)

	if w2.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("expected CORS headers to be set when Origin is present")
	}
}

func TestReadinessIgnoresProviderCredentialAvailability(t *testing.T) {
	server := newTestServer(t, &Config{Port: 8080})
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	recorder := httptest.NewRecorder()

	server.Router().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if response.Status != "ok" {
		t.Fatalf("readiness = %q, want ok", response.Status)
	}
}

func TestShutdown(t *testing.T) {
	config := &Config{
		Port:            0, // Use random port
		ShutdownTimeout: 5 * time.Second,
	}

	server := newTestServer(t, config)

	// Start server in background
	go func() {
		server.Start()
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := server.Shutdown(ctx)
	if err != nil {
		t.Errorf("expected successful shutdown, got error: %v", err)
	}
}

func TestShutdownDoesNotCloseApplicationStorage(t *testing.T) {
	config := &Config{
		Port:            0,
		ShutdownTimeout: 5 * time.Second,
	}

	store := storage.NewMockStore()
	server := newTestServer(t, config, withTestStore(store))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("expected successful shutdown, got error: %v", err)
	}

	if err := store.Set(context.Background(), "still-open", []byte("value")); err != nil {
		t.Fatalf("expected server shutdown to leave storage ownership with app: %v", err)
	}
}

func TestCORSHeaders(t *testing.T) {
	config := &Config{
		Port: 8080,
		CORS: CORSConfig{
			AllowedOrigins:   []string{"https://example.com"},
			AllowedMethods:   []string{"GET", "POST"},
			AllowedHeaders:   []string{"Content-Type", "Authorization"},
			AllowCredentials: true,
			MaxAge:           3600,
		},
	}

	server := newTestServer(t, config)

	// Test preflight request
	req := httptest.NewRequest("OPTIONS", "/api/v1/", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")

	w := httptest.NewRecorder()
	server.router.ServeHTTP(w, req)

	// Check CORS headers in response
	if w.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Errorf("expected CORS origin header to be https://example.com, got %s", w.Header().Get("Access-Control-Allow-Origin"))
	}

	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("expected CORS credentials header to be true")
	}
}
