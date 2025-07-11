package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler_Live(t *testing.T) {
	handler := NewHealthHandler("starport", "v1.0.0")

	req := httptest.NewRequest("GET", "/health/live", nil)
	w := httptest.NewRecorder()

	handler.Live(w, req)

	// Check status code
	assert.Equal(t, http.StatusOK, w.Code)

	// Parse response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Check response fields
	assert.Equal(t, "ok", resp["status"])
	assert.Equal(t, "starport", resp["service"])
	assert.Equal(t, "v1.0.0", resp["version"])

	// Check timestamp format
	timestamp, ok := resp["timestamp"].(string)
	assert.True(t, ok)
	_, err = time.Parse(time.RFC3339, timestamp)
	assert.NoError(t, err)
}

func TestHealthHandler_Ready(t *testing.T) {
	handler := NewHealthHandler("starport", "v1.0.0")

	req := httptest.NewRequest("GET", "/health/ready", nil)
	w := httptest.NewRecorder()

	handler.Ready(w, req)

	// Check status code
	assert.Equal(t, http.StatusOK, w.Code)

	// Parse response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Check response fields
	assert.Equal(t, "ok", resp["status"])
	assert.Equal(t, "starport", resp["service"])
	assert.Equal(t, "v1.0.0", resp["version"])

	// Check timestamp format
	timestamp, ok := resp["timestamp"].(string)
	assert.True(t, ok)
	_, err = time.Parse(time.RFC3339, timestamp)
	assert.NoError(t, err)
}

func TestHealthHandler_ContentType(t *testing.T) {
	handler := NewHealthHandler("starport", "v1.0.0")

	tests := []struct {
		name     string
		endpoint string
		handler  http.HandlerFunc
	}{
		{
			name:     "live endpoint",
			endpoint: "/health/live",
			handler:  handler.Live,
		},
		{
			name:     "ready endpoint",
			endpoint: "/health/ready",
			handler:  handler.Ready,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.endpoint, nil)
			w := httptest.NewRecorder()

			tt.handler(w, req)

			// Check content type
			contentType := w.Header().Get("Content-Type")
			assert.Equal(t, "application/json", contentType)
		})
	}
}

func TestHealthHandler_Timestamps(t *testing.T) {
	handler := NewHealthHandler("starport", "v1.0.0")

	// Get timestamp from live endpoint
	req1 := httptest.NewRequest("GET", "/health/live", nil)
	w1 := httptest.NewRecorder()
	handler.Live(w1, req1)

	var resp1 map[string]interface{}
	err := json.Unmarshal(w1.Body.Bytes(), &resp1)
	require.NoError(t, err)

	timestamp1, err := time.Parse(time.RFC3339, resp1["timestamp"].(string))
	require.NoError(t, err)

	// Small delay to ensure different timestamps
	time.Sleep(1100 * time.Millisecond) // Sleep over 1 second to ensure different RFC3339 timestamps

	// Get timestamp from ready endpoint
	req2 := httptest.NewRequest("GET", "/health/ready", nil)
	w2 := httptest.NewRecorder()
	handler.Ready(w2, req2)

	var resp2 map[string]interface{}
	err = json.Unmarshal(w2.Body.Bytes(), &resp2)
	require.NoError(t, err)

	timestamp2, err := time.Parse(time.RFC3339, resp2["timestamp"].(string))
	require.NoError(t, err)

	// Timestamps should be different
	assert.NotEqual(t, timestamp1, timestamp2)
}

func TestHealthHandler_ServiceInfo(t *testing.T) {
	tests := []struct {
		name    string
		service string
		version string
	}{
		{
			name:    "default values",
			service: "starport",
			version: "v1.0.0",
		},
		{
			name:    "custom service name",
			service: "custom-gateway",
			version: "v2.0.0",
		},
		{
			name:    "empty values",
			service: "",
			version: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHealthHandler(tt.service, tt.version)

			// Test live endpoint
			req := httptest.NewRequest("GET", "/health/live", nil)
			w := httptest.NewRecorder()
			handler.Live(w, req)

			var resp map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err)

			if tt.service == "" {
				assert.NotContains(t, resp, "service")
			} else {
				assert.Equal(t, tt.service, resp["service"])
			}
			if tt.version == "" {
				assert.NotContains(t, resp, "version")
			} else {
				assert.Equal(t, tt.version, resp["version"])
			}

			// Test ready endpoint
			req = httptest.NewRequest("GET", "/health/ready", nil)
			w = httptest.NewRecorder()
			handler.Ready(w, req)

			err = json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err)

			if tt.service == "" {
				assert.NotContains(t, resp, "service")
			} else {
				assert.Equal(t, tt.service, resp["service"])
			}
			if tt.version == "" {
				assert.NotContains(t, resp, "version")
			} else {
				assert.Equal(t, tt.version, resp["version"])
			}
		})
	}
}
