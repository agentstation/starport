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

func TestAdminKeySetsLimitsAndExpiry(t *testing.T) {
	handler, repository := newAdminTestController(t)

	body := `{
		"name": "budgeted",
		"scopes": ["chat:write"],
		"allowed_models": ["groq/compound-mini"],
		"expires_at": "2027-01-01T00:00:00Z",
		"limits": {
			"requests": {"limit": 5, "window_seconds": 60},
			"spend": {"limit": 1000000000, "interval": "day"},
			"tokens": {"limit": 250000, "interval": "month"}
		}
	}`
	req := httptest.NewRequest("POST", "/api/v1/admin/keys", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	handler.CreateKey(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var created struct {
		Key struct {
			ID            string           `json:"id"`
			AllowedModels []string         `json:"allowed_models"`
			Limits        *identity.Limits `json:"limits"`
			ExpiresAt     string           `json:"expires_at"`
		} `json:"key"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotNil(t, created.Key.Limits)
	assert.Equal(t, []string{"groq/compound-mini"}, created.Key.AllowedModels)
	assert.Equal(t, int64(5), created.Key.Limits.Requests.Limit)
	assert.Equal(t, int64(60), created.Key.Limits.Requests.WindowSeconds)
	assert.Equal(t, int64(1_000_000_000), created.Key.Limits.Spend.Limit)
	assert.Equal(t, "day", created.Key.Limits.Spend.Interval)
	assert.Equal(t, int64(250_000), created.Key.Limits.Tokens.Limit)
	assert.Equal(t, "month", created.Key.Limits.Tokens.Interval)
	assert.Contains(t, created.Key.ExpiresAt, "2027-01-01")

	// The limits persist on the durable record.
	record, err := repository.GetByID(context.Background(), created.Key.ID)
	require.NoError(t, err)
	require.NotNil(t, record.APIKey.Limits)
	assert.Equal(t, int64(1_000_000_000), record.APIKey.Limits.Spend.Limit)
	require.NotNil(t, record.APIKey.ExpiresAt)

	// Update changes limits and expiry on the same key.
	update := `{
		"limits": {"spend": {"limit": 2000000000, "interval": "week"}},
		"expires_at": "2028-01-01T00:00:00Z"
	}`
	updateReq := httptest.NewRequest("PUT", "/api/v1/admin/keys/"+created.Key.ID, bytes.NewBufferString(update))
	updateReq = withChiURLParam(updateReq, "key_id", created.Key.ID)
	updateW := httptest.NewRecorder()
	handler.UpdateKey(updateW, updateReq)
	require.Equal(t, http.StatusOK, updateW.Code, updateW.Body.String())

	updated, err := repository.GetByID(context.Background(), created.Key.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.APIKey.Limits)
	assert.Equal(t, int64(2_000_000_000), updated.APIKey.Limits.Spend.Limit)
	assert.Equal(t, "week", updated.APIKey.Limits.Spend.Interval)
	assert.Nil(t, updated.APIKey.Limits.Requests, "replacement limits omit the request override")
	assert.Contains(t, updated.APIKey.ExpiresAt.String(), "2028-01-01")

	// Clearing with an explicit empty object removes every limit.
	clearReq := httptest.NewRequest("PUT", "/api/v1/admin/keys/"+created.Key.ID, bytes.NewBufferString(`{"limits": {}}`))
	clearReq = withChiURLParam(clearReq, "key_id", created.Key.ID)
	clearW := httptest.NewRecorder()
	handler.UpdateKey(clearW, clearReq)
	require.Equal(t, http.StatusOK, clearW.Code, clearW.Body.String())

	cleared, err := repository.GetByID(context.Background(), created.Key.ID)
	require.NoError(t, err)
	assert.Nil(t, cleared.APIKey.Limits)
}

func TestAdminKeyRejectsInvalidLimits(t *testing.T) {
	handler, _ := newAdminTestController(t)

	body := `{
		"name": "broken",
		"scopes": ["chat:write"],
		"limits": {"spend": {"limit": 100, "interval": "hour"}}
	}`
	req := httptest.NewRequest("POST", "/api/v1/admin/keys", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	handler.CreateKey(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "interval")
}

func TestKeyListPagination(t *testing.T) {
	handler, repository := newAdminTestController(t)
	for _, id := range []string{"key-a", "key-b", "key-c", "key-d", "key-e"} {
		createAdminTestIdentity(t, repository, identity.APIKey{ID: id, Name: id, Active: true})
	}

	page := func(query string) (keys []map[string]any, pagination map[string]any) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/admin/keys"+query, nil)
		w := httptest.NewRecorder()
		handler.ListKeys(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp struct {
			Keys       []map[string]any `json:"keys"`
			Pagination map[string]any   `json:"pagination"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp.Keys, resp.Pagination
	}

	first, firstPagination := page("?limit=2")
	require.Len(t, first, 2)
	assert.Equal(t, "key-a", first[0]["id"])
	assert.Equal(t, "key-b", first[1]["id"])
	assert.Equal(t, true, firstPagination["has_more"])

	second, secondPagination := page("?limit=2&offset=2")
	require.Len(t, second, 2)
	assert.Equal(t, "key-c", second[0]["id"])
	assert.Equal(t, "key-d", second[1]["id"])
	assert.Equal(t, true, secondPagination["has_more"])

	last, lastPagination := page("?limit=2&offset=4")
	require.Len(t, last, 1)
	assert.Equal(t, "key-e", last[0]["id"])
	assert.Equal(t, false, lastPagination["has_more"])

	// An invalid parameter is a caller error, not a silent default.
	badReq := httptest.NewRequest("GET", "/api/v1/admin/keys?limit=nope", nil)
	badW := httptest.NewRecorder()
	handler.ListKeys(badW, badReq)
	require.Equal(t, http.StatusBadRequest, badW.Code)
}

func withChiURLParam(req *http.Request, key, value string) *http.Request {
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
}
