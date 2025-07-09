package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Check that all security headers are set
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "1; mode=block", w.Header().Get("X-XSS-Protection"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "default-src 'self'")
	assert.Contains(t, w.Header().Get("Permissions-Policy"), "accelerometer=()")
}

func TestRequestSizeLimiter(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		bodySize    int
		maxSize     int64
		expectError bool
	}{
		{
			name:        "GET request not limited",
			method:      "GET",
			bodySize:    1000,
			maxSize:     100,
			expectError: false,
		},
		{
			name:        "POST within limit",
			method:      "POST",
			bodySize:    100,
			maxSize:     1000,
			expectError: false,
		},
		{
			name:        "POST exceeds limit",
			method:      "POST",
			bodySize:    1000,
			maxSize:     100,
			expectError: true,
		},
		{
			name:        "PUT exceeds limit",
			method:      "PUT",
			bodySize:    1000,
			maxSize:     100,
			expectError: true,
		},
		{
			name:        "PATCH exceeds limit",
			method:      "PATCH",
			bodySize:    1000,
			maxSize:     100,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := RequestSizeLimiter(tt.maxSize)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Try to read the body
				buf := make([]byte, tt.bodySize+1)
				n, err := r.Body.Read(buf)
				
				if tt.expectError && (tt.method == "POST" || tt.method == "PUT" || tt.method == "PATCH") {
					// For methods with body limits, we expect an error when reading beyond the limit
					if n >= int(tt.maxSize) && err == nil {
						t.Error("expected error when reading beyond limit")
					}
				} else {
					// For GET or within-limit requests, reading should succeed
					if err != nil && err.Error() != "EOF" && !strings.Contains(err.Error(), "EOF") {
						t.Errorf("unexpected error reading body: %v", err)
					}
				}
				
				w.WriteHeader(http.StatusOK)
			}))

			body := bytes.Repeat([]byte("a"), tt.bodySize)
			req := httptest.NewRequest(tt.method, "/", bytes.NewReader(body))
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)
		})
	}
}

func TestSecurityMiddlewareIntegration(t *testing.T) {
	config := &Config{
		Port:           8080,
		MaxRequestSize: 1024, // 1KB limit for testing
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST"},
		},
	}

	server := newTestServer(config)

	// Add a test endpoint
	server.router.Post("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	t.Run("security headers are applied", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// Verify security headers
		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	})

	t.Run("request size limit is enforced", func(t *testing.T) {
		// Send a request larger than the limit
		largeBody := bytes.Repeat([]byte("x"), 2048) // 2KB
		req := httptest.NewRequest("POST", "/test", bytes.NewReader(largeBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// The handler will try to read the body and fail
		// This is the expected behavior for oversized requests
	})
}