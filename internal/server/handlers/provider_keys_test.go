package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/providers"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockKeyManager implements providers.KeyManager for testing
type mockKeyManager struct{}

func (m *mockKeyManager) AddKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]interface{}, isFallback bool, priority int) (*models.ProviderKey, error) {
	return &models.ProviderKey{
		Scope:    scope,
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) GetKey(ctx context.Context, scope, provider string) (*models.ProviderKey, error) {
	return &models.ProviderKey{
		Scope:    scope,
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) GetKeys(ctx context.Context, scope, provider string) ([]*models.ProviderKey, error) {
	return []*models.ProviderKey{
		{
			Scope:    scope,
			Provider: provider,
		},
	}, nil
}

func (m *mockKeyManager) ListKeys(ctx context.Context, scope string) ([]*models.ProviderKey, error) {
	return []*models.ProviderKey{
		{
			Scope:    scope,
			Provider: "openai",
		},
	}, nil
}

func (m *mockKeyManager) UpdateKey(ctx context.Context, scope, provider string, key map[string]string, config map[string]interface{}, isFallback *bool, priority *int) (*models.ProviderKey, error) {
	return &models.ProviderKey{
		Scope:    scope,
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) DeleteKey(ctx context.Context, scope, provider string) error {
	return nil
}

func (m *mockKeyManager) ValidateKey(ctx context.Context, provider string, key map[string]string, config map[string]interface{}) error {
	return nil
}

func (m *mockKeyManager) AddGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]interface{}, rateLimit *models.RateLimitConfig) (*models.ProviderKey, error) {
	return &models.ProviderKey{
		Scope:    "*",
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) GetGlobalKey(ctx context.Context, provider string) (*models.ProviderKey, error) {
	return &models.ProviderKey{
		Scope:    "*",
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) UpdateGlobalKey(ctx context.Context, provider string, key map[string]string, config map[string]interface{}, rateLimit *models.RateLimitConfig) (*models.ProviderKey, error) {
	return &models.ProviderKey{
		Scope:    "*",
		Provider: provider,
	}, nil
}

func (m *mockKeyManager) DeleteGlobalKey(ctx context.Context, provider string) error {
	return nil
}

func (m *mockKeyManager) ListGlobalKeys(ctx context.Context) ([]*models.ProviderKey, error) {
	return []*models.ProviderKey{
		{
			Scope:    "*",
			Provider: "openai",
		},
	}, nil
}

func (m *mockKeyManager) DetermineKeyStrategy(ctx context.Context, scope string, provider string) providers.FallbackStrategy {
	return providers.GatewayFirst
}

func (m *mockKeyManager) CalculateProviderKeyCost(usage *providers.Usage) float64 {
	return 0.0
}

func (m *mockKeyManager) RecordUsage(ctx context.Context, scope string, provider string, usage *providers.Usage) error {
	return nil
}

func (m *mockKeyManager) RotateEncryptionKey(ctx context.Context) error {
	return nil
}

func TestProviderKeysHandler_List(t *testing.T) {
	handler := NewProviderKeysHandler(&mockKeyManager{})

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Get("/api/v1/keys/{key_id}/provider-keys", handler.List)

	req := httptest.NewRequest("GET", "/api/v1/keys/test-key/provider-keys", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Check status code
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "provider_keys")
}

func TestProviderKeysHandler_Create(t *testing.T) {
	handler := NewProviderKeysHandler(&mockKeyManager{})

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Post("/api/v1/keys/{key_id}/provider-keys", handler.Create)

	body := map[string]interface{}{
		"provider": "openai",
		"credentials": map[string]string{
			"api_key": "sk-test123",
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/v1/keys/test-key/provider-keys", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Check status code
	assert.Equal(t, http.StatusCreated, w.Code)

	// Check response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "message")
}

func TestProviderKeysHandler_Get(t *testing.T) {
	handler := NewProviderKeysHandler(&mockKeyManager{})

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Get("/api/v1/keys/{key_id}/provider-keys/{provider}", handler.Get)

	req := httptest.NewRequest("GET", "/api/v1/keys/test-key/provider-keys/openai", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Check status code
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "provider")
}

func TestProviderKeysHandler_Update(t *testing.T) {
	handler := NewProviderKeysHandler(&mockKeyManager{})

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Put("/api/v1/keys/{key_id}/provider-keys/{provider}", handler.Update)

	body := map[string]interface{}{
		"credentials": map[string]string{
			"api_key": "sk-updated123",
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("PUT", "/api/v1/keys/test-key/provider-keys/openai", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Check status code
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "message")
}

func TestProviderKeysHandler_Delete(t *testing.T) {
	handler := NewProviderKeysHandler(&mockKeyManager{})

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Delete("/api/v1/keys/{key_id}/provider-keys/{provider}", handler.Delete)

	req := httptest.NewRequest("DELETE", "/api/v1/keys/test-key/provider-keys/openai", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Check status code
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "message")
}

func TestProviderKeysHandler_GetUsage(t *testing.T) {
	handler := NewProviderKeysHandler(&mockKeyManager{})

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Get("/api/v1/keys/{key_id}/usage/provider-keys", handler.GetUsage)

	req := httptest.NewRequest("GET", "/api/v1/keys/test-key/usage/provider-keys", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Check status code - should return not implemented
	assert.Equal(t, http.StatusNotImplemented, w.Code)

	// Check response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp["message"], "Provider key usage analytics not yet implemented")
}

func TestProviderKeysHandler_GetUsageComparison(t *testing.T) {
	handler := NewProviderKeysHandler(&mockKeyManager{})

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Get("/api/v1/keys/{key_id}/usage/comparison", handler.GetUsageComparison)

	req := httptest.NewRequest("GET", "/api/v1/keys/test-key/usage/comparison", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Check status code - should return not implemented
	assert.Equal(t, http.StatusNotImplemented, w.Code)

	// Check response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp["message"], "Usage comparison not yet implemented")
}

func TestProviderKeysHandler_ContentType(t *testing.T) {
	handler := NewProviderKeysHandler(&mockKeyManager{})

	tests := []struct {
		name    string
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{
			name:    "list",
			method:  "GET",
			path:    "/api/v1/keys/test-key/provider-keys",
			handler: handler.List,
		},
		{
			name:    "create",
			method:  "POST",
			path:    "/api/v1/keys/test-key/provider-keys",
			handler: handler.Create,
		},
		{
			name:    "get",
			method:  "GET",
			path:    "/api/v1/keys/test-key/provider-keys/openai",
			handler: handler.Get,
		},
		{
			name:    "update",
			method:  "PUT",
			path:    "/api/v1/keys/test-key/provider-keys/openai",
			handler: handler.Update,
		},
		{
			name:    "delete",
			method:  "DELETE",
			path:    "/api/v1/keys/test-key/provider-keys/openai",
			handler: handler.Delete,
		},
		{
			name:    "get usage",
			method:  "GET",
			path:    "/api/v1/keys/test-key/usage/provider-keys",
			handler: handler.GetUsage,
		},
		{
			name:    "get usage comparison",
			method:  "GET",
			path:    "/api/v1/keys/test-key/usage/comparison",
			handler: handler.GetUsageComparison,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request with appropriate body for POST/PUT
			var req *http.Request
			if tt.method == "POST" || tt.method == "PUT" {
				body := map[string]interface{}{"test": "data"}
				bodyBytes, _ := json.Marshal(body)
				req = httptest.NewRequest(tt.method, tt.path, bytes.NewReader(bodyBytes))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}

			w := httptest.NewRecorder()

			// Handle request
			tt.handler(w, req)

			// Check content type
			contentType := w.Header().Get("Content-Type")
			assert.Equal(t, "application/json", contentType)
		})
	}
}
