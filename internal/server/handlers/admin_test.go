package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/models"
	"github.com/agentstation/starport/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStore implements storage.KVStore for testing
type mockStore struct {
	data map[string][]byte
}

func newMockStore() *mockStore {
	return &mockStore{
		data: make(map[string][]byte),
	}
}

func (m *mockStore) Get(ctx context.Context, key string) ([]byte, error) {
	if val, ok := m.data[key]; ok {
		return val, nil
	}
	return nil, storage.ErrNotFound
}

func (m *mockStore) Set(ctx context.Context, key string, value []byte) error {
	m.data[key] = value
	return nil
}

func (m *mockStore) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	m.data[key] = value
	return nil
}

func (m *mockStore) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockStore) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockStore) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	return 0, nil
}

func (m *mockStore) ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error) {
	var keys []string
	for k := range m.data {
		if len(keys) >= limit {
			break
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *mockStore) Close() error {
	return nil
}

func (m *mockStore) BeginTransaction(ctx context.Context) (storage.Transaction, error) {
	return nil, nil
}

func (m *mockStore) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	return 0, nil
}

func (m *mockStore) ExpireAt(ctx context.Context, key string, expireAt time.Time) error {
	return nil
}

func (m *mockStore) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	return 0, nil
}

func (m *mockStore) CompareAndSwap(ctx context.Context, key string, old, newValue []byte) error {
	return nil
}

func (m *mockStore) BatchGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for _, key := range keys {
		if val, ok := m.data[key]; ok {
			result[key] = val
		}
	}
	return result, nil
}

func (m *mockStore) BatchSet(ctx context.Context, items map[string][]byte) error {
	for k, v := range items {
		m.data[k] = v
	}
	return nil
}

func (m *mockStore) BatchDelete(ctx context.Context, keys []string) error {
	for _, key := range keys {
		delete(m.data, key)
	}
	return nil
}

func (m *mockStore) BatchSetWithTTL(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	for k, v := range items {
		m.data[k] = v
	}
	return nil
}

func (m *mockStore) Scan(ctx context.Context, pattern string, limit int) ([]string, error) {
	return m.ScanWithPrefix(ctx, "", limit)
}

func (m *mockStore) Ping(ctx context.Context) error {
	return nil
}

func TestAdminHandler_SystemInfo(t *testing.T) {
	// Create handler
	store := newMockStore()
	handler := NewAdminHandler(store)

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/admin/info", nil)
	w := httptest.NewRecorder()

	// Call handler
	handler.SystemInfo(w, req)

	// Check status
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response structure
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "starport", resp["service"])
	assert.Contains(t, resp, "version")
}

func TestAdminHandler_Metrics(t *testing.T) {
	// Create handler
	store := newMockStore()
	handler := NewAdminHandler(store)

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/admin/metrics", nil)
	w := httptest.NewRecorder()

	// Call handler
	handler.Metrics(w, req)

	// Check status
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response structure
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "requests")
	assert.Contains(t, resp, "latency")
}

func TestAdminHandler_ListKeys(t *testing.T) {
	// Create handler
	store := newMockStore()
	handler := NewAdminHandler(store)

	// Add some test keys
	apiKey := &models.APIKey{
		ID:     "test-key-1",
		Name:   "Test Key 1",
		Active: true,
	}
	keyData, _ := storage.Serialize(apiKey)
	_ = store.Set(context.Background(), storage.APIKeyKey(apiKey.ID), keyData)

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/admin/keys", nil)
	w := httptest.NewRecorder()

	// Call handler
	handler.ListKeys(w, req)

	// Check status
	assert.Equal(t, http.StatusOK, w.Code)

	// Check response
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "keys")
	assert.Contains(t, resp, "count")
}

func TestAdminHandler_CreateKey(t *testing.T) {
	// Create handler
	store := newMockStore()
	handler := NewAdminHandler(store)

	// Create request body
	reqBody := map[string]interface{}{
		"name":        "Test API Key",
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
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "key")
	assert.Contains(t, resp, "message")

	keyInfo := resp["key"].(map[string]interface{})
	assert.Contains(t, keyInfo, "key") // The actual key value
	assert.Equal(t, "Test API Key", keyInfo["name"])
}

func TestAdminHandler_GetKey(t *testing.T) {
	// Create handler
	store := newMockStore()
	handler := NewAdminHandler(store)

	// Add a test key
	apiKey := &models.APIKey{
		ID:     "test-key-1",
		Name:   "Test Key 1",
		Active: true,
		Hash:   "secret-hash",
	}
	keyData, _ := storage.Serialize(apiKey)
	_ = store.Set(context.Background(), storage.APIKeyKey(apiKey.ID), keyData)

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
	var resp models.APIKey
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "test-key-1", resp.ID)
	assert.Equal(t, "Test Key 1", resp.Name)
	assert.Empty(t, resp.Hash) // Hash should not be exposed
}

func TestAdminHandler_DeleteKey(t *testing.T) {
	// Create handler
	store := newMockStore()
	handler := NewAdminHandler(store)

	// Add a test key
	apiKey := &models.APIKey{
		ID:     "test-key-1",
		Name:   "Test Key 1",
		Active: true,
	}
	keyData, _ := storage.Serialize(apiKey)
	_ = store.Set(context.Background(), storage.APIKeyKey(apiKey.ID), keyData)

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
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "message")
	assert.Equal(t, "test-key-1", resp["key_id"])

	// Verify key was deleted
	_, err = store.Get(context.Background(), storage.APIKeyKey(apiKey.ID))
	assert.Equal(t, storage.ErrNotFound, err)
}
