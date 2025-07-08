package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleLive(t *testing.T) {
	config := &Config{Port: 8080}
	server := newTestServer(config)

	req := httptest.NewRequest("GET", "/health/live", nil)
	w := httptest.NewRecorder()

	server.handleLive(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected content type application/json, got %s", contentType)
	}

	// Parse response
	var response HealthResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	if err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check response fields
	if response.Status != "ok" {
		t.Errorf("expected status 'ok', got '%s'", response.Status)
	}

	if response.Timestamp == "" {
		t.Error("expected timestamp to be set")
	}

	// Verify timestamp format
	_, err = time.Parse(time.RFC3339, response.Timestamp)
	if err != nil {
		t.Errorf("invalid timestamp format: %v", err)
	}
}

func TestHandleReady(t *testing.T) {
	config := &Config{Port: 8080}
	server := newTestServer(config)

	req := httptest.NewRequest("GET", "/health/ready", nil)
	w := httptest.NewRecorder()

	server.handleReady(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected content type application/json, got %s", contentType)
	}

	// Parse response
	var response HealthResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	if err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check response fields
	if response.Status != "ok" {
		t.Errorf("expected status 'ok', got '%s'", response.Status)
	}

	if response.Timestamp == "" {
		t.Error("expected timestamp to be set")
	}

	if response.Version != "dev" {
		t.Errorf("expected version 'dev', got '%s'", response.Version)
	}

	// Check details
	if response.Details == nil {
		t.Error("expected details to be set")
	} else {
		if response.Details["go_version"] == "" {
			t.Error("expected go_version in details")
		}
		if response.Details["goroutines"] == "" {
			t.Error("expected goroutines in details")
		}
	}
}

func TestHealthEndpointsIntegration(t *testing.T) {
	config := &Config{Port: 8080}
	server := newTestServer(config)

	tests := []struct {
		name     string
		endpoint string
	}{
		{"liveness", "/health/live"},
		{"readiness", "/health/ready"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.endpoint, nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
			}

			// Verify JSON response
			var response map[string]interface{}
			err := json.NewDecoder(w.Body).Decode(&response)
			if err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if response["status"] != "ok" {
				t.Errorf("expected status 'ok', got '%v'", response["status"])
			}
		})
	}
}