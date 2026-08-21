package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAdminTestController(t *testing.T) (*AdminController, identity.Repository) {
	t.Helper()
	repository, err := identity.Open(storage.NewMockStore())
	require.NoError(t, err)
	usageRecords, err := usage.Open(storage.NewMockStore(), usage.Options{})
	require.NoError(t, err)
	return NewAdminController(repository, usageRecords), repository
}

func createAdminTestIdentity(t *testing.T, repository identity.Repository, apiKey identity.APIKey) {
	t.Helper()
	if len(apiKey.Scopes) == 0 {
		apiKey.Scopes = []string{"test"}
	}
	if apiKey.Hash == "" {
		apiKey.Hash = "hash-" + apiKey.ID
	}
	_, err := repository.Create(context.Background(), apiKey)
	require.NoError(t, err)
}

func TestAdminHandler_SystemInfo(t *testing.T) {
	// Create handler
	handler, _ := newAdminTestController(t)

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/admin/info", nil)
	w := httptest.NewRecorder()

	// Call handler
	handler.SystemInfo(w, req)

	// Check status
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response structure
	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "starport", resp["service"])
	assert.Contains(t, resp, "version")
	assert.Equal(t, runtime.Version(), resp["go_version"])
	assert.Equal(t, runtime.GOOS, resp["os"])
	assert.Equal(t, runtime.GOARCH, resp["arch"])
	assert.NotContains(t, w.Body.String(), "TODO")
}

func TestAdminHandler_Metrics(t *testing.T) {
	// Create handler
	handler, _ := newAdminTestController(t)

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/admin/metrics", nil)
	w := httptest.NewRecorder()

	// Call handler
	handler.Metrics(w, req)

	// Check status
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response structure
	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "requests")
	assert.Contains(t, resp, "latency")
}

func TestAdminHandler_ListKeys(t *testing.T) {
	// Create handler
	handler, identities := newAdminTestController(t)

	// Add some test keys
	apiKey := &identity.APIKey{
		ID:     "test-key-1",
		Name:   "Test-Key-1",
		Active: true,
	}
	createAdminTestIdentity(t, identities, *apiKey)

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/admin/keys", nil)
	w := httptest.NewRecorder()

	// Call handler
	handler.ListKeys(w, req)

	// Check status
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response
	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "keys")
	assert.Contains(t, resp, "count")
}

func TestAdminHandler_CreateKey(t *testing.T) {
	// Create handler
	handler, _ := newAdminTestController(t)

	// Create request body
	reqBody := map[string]any{
		"name":        "Test-API-Key",
		"description": "Test key for unit tests",
		"scopes":      []string{"read", "write"},
	}
	body, _ := json.Marshal(reqBody)

	// Create request
	req := httptest.NewRequest("POST", "/api/v1/admin/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Call handler
	handler.CreateKey(w, req)

	// Check status
	assert.Equal(t, http.StatusCreated, w.Code)

	// Check response
	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "key")
	assert.Contains(t, resp, "message")

	keyInfo := resp["key"].(map[string]any)
	assert.Contains(t, keyInfo, "key") // The actual key value
	assert.Equal(t, "Test-API-Key", keyInfo["name"])
}

func TestAdminHandler_GetKey(t *testing.T) {
	// Create handler
	handler, identities := newAdminTestController(t)

	// Add a test key
	apiKey := &identity.APIKey{
		ID:     "test-key-1",
		Name:   "Test-Key-1",
		Active: true,
		Hash:   "secret-hash",
	}
	createAdminTestIdentity(t, identities, *apiKey)

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Get("/api/v1/admin/keys/{key_id}", handler.GetKey)

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/admin/keys/test-key-1", nil)
	w := httptest.NewRecorder()

	// Handle request
	r.ServeHTTP(w, req)

	// Check status
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response
	var resp identity.APIKey
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "test-key-1", resp.ID)
	assert.Equal(t, "Test-Key-1", resp.Name)
	assert.Empty(t, resp.Hash) // Hash should not be exposed
}

func TestAdminHandler_DeleteKey(t *testing.T) {
	// Create handler
	handler, identities := newAdminTestController(t)

	// Add a test key
	apiKey := &identity.APIKey{
		ID:     "test-key-1",
		Name:   "Test-Key-1",
		Active: true,
	}
	createAdminTestIdentity(t, identities, *apiKey)

	// Create router to handle URL params
	r := chi.NewRouter()
	r.Delete("/api/v1/admin/keys/{key_id}", handler.DeleteKey)

	// Create request
	req := httptest.NewRequest("DELETE", "/api/v1/admin/keys/test-key-1", nil)
	w := httptest.NewRecorder()

	// Handle request
	r.ServeHTTP(w, req)

	// Check status
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response
	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "message")
	assert.Equal(t, "test-key-1", resp["key_id"])

	// Verify key was deleted
	_, err = identities.GetByID(context.Background(), apiKey.ID)
	assert.ErrorIs(t, err, identity.ErrNotFound)
}
