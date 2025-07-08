package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

	server := New(config)
	if server == nil {
		t.Fatal("expected server to be created")
	}

	if server.config != config {
		t.Error("expected server config to match")
	}

	if server.router == nil {
		t.Error("expected router to be initialized")
	}

	if server.httpServer == nil {
		t.Error("expected HTTP server to be initialized")
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

	server := New(config)
	
	// Test that middleware is properly configured
	req := httptest.NewRequest("GET", "/api/v1/", nil)
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// CORS headers are only set when Origin header is present in request
	// Let's test with Origin header
	req2 := httptest.NewRequest("GET", "/api/v1/", nil)
	req2.Header.Set("Origin", "http://localhost:3000")
	w2 := httptest.NewRecorder()
	
	server.router.ServeHTTP(w2, req2)
	
	if w2.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("expected CORS headers to be set when Origin is present")
	}
}

func TestShutdown(t *testing.T) {
	config := &Config{
		Port:            0, // Use random port
		ShutdownTimeout: 5 * time.Second,
	}

	server := New(config)

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

	server := New(config)

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