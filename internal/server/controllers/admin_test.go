package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/events"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAdminTestController(t *testing.T, options ...AdminOption) (*AdminController, apikey.Repository) {
	t.Helper()
	repository, err := apikey.Open(storage.NewMockStore())
	require.NoError(t, err)
	usageRecords, err := usage.Open(storage.NewMockStore(), usage.Options{})
	require.NoError(t, err)
	return NewAdminController(repository, newAdminTestAccounts(t), usageRecords, options...), repository
}

// newAdminTestAccounts returns an account repository holding the canonical account,
// which is what every key issued without an explicit account resolves to.
func newAdminTestAccounts(t *testing.T) account.Repository {
	t.Helper()
	accounts, err := account.Open(storage.NewMockStore())
	require.NoError(t, err)
	_, err = accounts.EnsureDefault(context.Background())
	require.NoError(t, err)
	return accounts
}

func createAdminTestAPIKey(t *testing.T, repository apikey.Repository, apiKey apikey.APIKey) {
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

// TestSystemInfoNamesTheFileBackend states the one fact about file storage
// that no file route reveals: where the bytes land. An operator reads it to
// confirm the destination without opening the process configuration.
func TestSystemInfoNamesTheFileBackend(t *testing.T) {
	repository, err := apikey.Open(storage.NewMockStore())
	require.NoError(t, err)
	handler := NewAdminController(repository, newAdminTestAccounts(t), nil,
		WithFileStorage("filesystem"))

	response := httptest.NewRecorder()
	handler.SystemInfo(response, httptest.NewRequest("GET", "/api/v1/admin/info", nil))

	var body struct {
		Files struct {
			Backend string `json:"backend"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "filesystem", body.Files.Backend)
}

// TestSystemInfoSaysWhenNoFileStorageExists states the other half. A
// deployment that stores no files reports that, rather than leaving the field
// out, because an absent field reads as an older gateway.
func TestSystemInfoSaysWhenNoFileStorageExists(t *testing.T) {
	handler, _ := newAdminTestController(t)

	response := httptest.NewRecorder()
	handler.SystemInfo(response, httptest.NewRequest("GET", "/api/v1/admin/info", nil))

	var body struct {
		Files struct {
			Backend string `json:"backend"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "not configured", body.Files.Backend)
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
	handler, apiKeys := newAdminTestController(t)

	// Add some test keys
	apiKey := &apikey.APIKey{
		ID:     "test-key-1",
		Name:   "Test-Key-1",
		Active: true,
	}
	createAdminTestAPIKey(t, apiKeys, *apiKey)

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
	handler, apiKeys := newAdminTestController(t)

	// Add a test key
	apiKey := &apikey.APIKey{
		ID:     "test-key-1",
		Name:   "Test-Key-1",
		Active: true,
		Hash:   "secret-hash",
	}
	createAdminTestAPIKey(t, apiKeys, *apiKey)

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
	var resp apikey.APIKey
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "test-key-1", resp.ID)
	assert.Equal(t, "Test-Key-1", resp.Name)
	assert.Empty(t, resp.Hash) // Hash should not be exposed
}

func TestAdminHandler_DeleteKey(t *testing.T) {
	// Create handler
	handler, apiKeys := newAdminTestController(t)

	// Add a test key
	apiKey := &apikey.APIKey{
		ID:     "test-key-1",
		Name:   "Test-Key-1",
		Active: true,
	}
	createAdminTestAPIKey(t, apiKeys, *apiKey)

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
	_, err = apiKeys.GetByID(context.Background(), apiKey.ID)
	assert.ErrorIs(t, err, apikey.ErrNotFound)
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
			ID            string         `json:"id"`
			AllowedModels []string       `json:"allowed_models"`
			Limits        *limits.Limits `json:"limits"`
			ExpiresAt     string         `json:"expires_at"`
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
		createAdminTestAPIKey(t, repository, apikey.APIKey{ID: id, Name: id, Active: true})
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

// createKeyRequest posts one key-creation body and returns the recorder.
func createKeyRequest(t *testing.T, handler *AdminController, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/keys", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.CreateKey(recorder, request)
	return recorder
}

// TestAdminCreateKeyReportsTheOwningAccount proves the account a key belongs to
// is visible on the wire. Without it an operator cannot tell which account's
// limits and credentials a key will resolve to.
func TestAdminCreateKeyReportsTheOwningAccount(t *testing.T) {
	handler, _ := newAdminTestController(t)

	recorder := createKeyRequest(t, handler, map[string]any{
		"name":   "Default-Account-Key",
		"scopes": []string{"chat:write"},
	})
	require.Equal(t, http.StatusCreated, recorder.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	keyInfo, ok := response["key"].(map[string]any)
	require.True(t, ok)
	// A key that names no account belongs to the canonical one, and the
	// response says so rather than leaving the field empty.
	assert.Equal(t, account.DefaultID, keyInfo["account_id"])
}

// TestAdminCreateKeyRefusesAnUnknownAccount is the AON2 acceptance case at the
// HTTP boundary. Naming an account that does not exist is the caller's mistake,
// so it answers 400 rather than 500 and stores nothing.
func TestAdminCreateKeyRefusesAnUnknownAccount(t *testing.T) {
	handler, apiKeys := newAdminTestController(t)

	recorder := createKeyRequest(t, handler, map[string]any{
		"name":       "Ghost-Key",
		"account_id": "does-not-exist",
		"scopes":     []string{"chat:write"},
	})
	assert.Equal(t, http.StatusBadRequest, recorder.Code)

	records, err := apiKeys.List(context.Background(), 10, 0)
	require.NoError(t, err)
	assert.Empty(t, records, "a refused creation must store no key")
}

// TestAdminCreateKeyRefusesAMalformedAccountID guards the credential storage
// key. An account ID becomes part of a credential scope, so a separator or a
// wildcard inside one must never reach storage.
func TestAdminCreateKeyRefusesAMalformedAccountID(t *testing.T) {
	handler, _ := newAdminTestController(t)

	for _, accountID := range []string{"acme corp", "acme/eu", "*", "account:acme"} {
		recorder := createKeyRequest(t, handler, map[string]any{
			"name":       "Bad-Key",
			"account_id": accountID,
			"scopes":     []string{"chat:write"},
		})
		assert.Equalf(t, http.StatusBadRequest, recorder.Code, "account %q", accountID)
	}
}

// TestSystemInfoReportsBuildVersion states what an operator asks first
// when a deployment misbehaves: which binary answers, and since when. The
// stamped values come through verbatim, and the clock supplies the rest.
func TestSystemInfoReportsBuildVersion(t *testing.T) {
	repository, err := apikey.Open(storage.NewMockStore())
	require.NoError(t, err)
	started := time.Now().Add(-90 * time.Second)
	handler := NewAdminController(repository, newAdminTestAccounts(t), nil,
		WithBuildInfo(BuildInfo{
			Version: "1.2.0", Commit: "abc1234", BuildTime: "2026-09-01T00:00:00Z",
			StartedAt: started,
		}),
		WithDeployment(Deployment{
			StorageMode: "badger", RelationalMode: "sqlite", MetricsMode: "admin",
			TracesEndpoint:  "https://collector@otel.example.com:4318/v1/traces",
			UsageExportKind: "http", UsageExport: dropCount(3),
			GuardrailChecks: []string{"pii"}, PIIMode: "refuse",
			AuditRetention: 48 * time.Hour,
		}))

	response := httptest.NewRecorder()
	handler.SystemInfo(response, httptest.NewRequest("GET", "/api/v1/admin/info", nil))

	var body struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildTime string `json:"build_time"`
		StartedAt string `json:"started_at"`
		Uptime    string `json:"uptime"`
		Storage   struct {
			Type       string `json:"type"`
			Relational string `json:"relational"`
		} `json:"storage"`
		Telemetry struct {
			Metrics string `json:"metrics"`
			Traces  struct {
				EndpointHost string `json:"endpoint_host"`
			} `json:"traces"`
			UsageExport struct {
				Kind    string `json:"kind"`
				Dropped int64  `json:"dropped"`
			} `json:"usage_export"`
		} `json:"telemetry"`
		Guardrails struct {
			Checks  []string `json:"checks"`
			PIIMode string   `json:"pii_mode"`
		} `json:"guardrails"`
		Retention struct {
			AuditSeconds int64 `json:"audit_seconds"`
			FilesSeconds int64 `json:"files_seconds"`
		} `json:"retention"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "1.2.0", body.Version)
	assert.Equal(t, "abc1234", body.Commit)
	assert.Equal(t, "2026-09-01T00:00:00Z", body.BuildTime)
	assert.Equal(t, started.UTC().Format(time.RFC3339), body.StartedAt)
	uptime, err := time.ParseDuration(body.Uptime)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, uptime, 90*time.Second)
	assert.Equal(t, "badger", body.Storage.Type)
	assert.Equal(t, "sqlite", body.Storage.Relational)
	assert.Equal(t, "admin", body.Telemetry.Metrics)
	assert.Equal(t, "otel.example.com:4318", body.Telemetry.Traces.EndpointHost)
	assert.Equal(t, "http", body.Telemetry.UsageExport.Kind)
	assert.Equal(t, int64(3), body.Telemetry.UsageExport.Dropped)
	assert.Equal(t, []string{"pii"}, body.Guardrails.Checks)
	assert.Equal(t, "refuse", body.Guardrails.PIIMode)
	assert.Equal(t, int64(172800), body.Retention.AuditSeconds)
	assert.Equal(t, int64(0), body.Retention.FilesSeconds)
}

// TestSystemInfoNamesAnUnstampedBuild covers the binary a developer built
// with go build: every provenance field reads dev, and the fields the
// composition root never supplied read unavailable rather than empty.
func TestSystemInfoNamesAnUnstampedBuild(t *testing.T) {
	handler, _ := newAdminTestController(t)

	response := httptest.NewRecorder()
	handler.SystemInfo(response, httptest.NewRequest("GET", "/api/v1/admin/info", nil))

	var body struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		StartedAt string `json:"started_at"`
		Uptime    string `json:"uptime"`
		Storage   struct {
			Type string `json:"type"`
		} `json:"storage"`
		Telemetry struct {
			Traces      *json.RawMessage `json:"traces"`
			UsageExport struct {
				Kind string `json:"kind"`
			} `json:"usage_export"`
		} `json:"telemetry"`
		Guardrails struct {
			Checks  []string `json:"checks"`
			PIIMode string   `json:"pii_mode"`
		} `json:"guardrails"`
		Webhooks struct {
			Configured bool `json:"configured"`
		} `json:"webhooks"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "dev", body.Version)
	assert.Equal(t, "dev", body.Commit)
	assert.Equal(t, "unavailable", body.StartedAt)
	assert.Equal(t, "unavailable", body.Uptime)
	assert.Equal(t, "unavailable", body.Storage.Type)
	assert.Nil(t, body.Telemetry.Traces)
	assert.Equal(t, "off", body.Telemetry.UsageExport.Kind)
	assert.Equal(t, []string{}, body.Guardrails.Checks)
	assert.Equal(t, "redact", body.Guardrails.PIIMode)
	assert.False(t, body.Webhooks.Configured)
}

// TestAdminWebhooksSummaryRedactsURL reads the webhook summary through a
// real dispatcher. The receiver comes back without its credential or
// query, the event list matches the guide, and the two events that
// never delivered count as dead letters.
func TestAdminWebhooksSummaryRedactsURL(t *testing.T) {
	dispatcher := events.NewDispatcher(
		[]string{"https://receiver@hooks.example.com/starport?ticket=t1"},
		"s", events.Options{MaxPending: 4},
	)
	require.NoError(t, dispatcher.Close(context.Background()))
	dispatcher.Emit(events.TypeKeyCreated, map[string]string{"key_id": "key_1"})
	dispatcher.Emit(events.TypeKeyDeleted, map[string]string{"key_id": "key_1"})

	handler, _ := newAdminTestController(t, WithWebhooks(dispatcher))
	response := httptest.NewRecorder()
	handler.Webhooks(response, httptest.NewRequest("GET", "/api/v1/admin/webhooks", nil))

	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Configured bool     `json:"configured"`
		Endpoints  []string `json:"endpoints"`
		Events     []string `json:"events"`
		Queue      struct {
			Depth    int `json:"depth"`
			Capacity int `json:"capacity"`
		} `json:"queue"`
		DeadLetters int64 `json:"dead_letters"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.True(t, body.Configured)
	assert.Equal(t, []string{"https://hooks.example.com/starport"}, body.Endpoints)
	assert.NotContains(t, response.Body.String(), "ticket")
	assert.NotContains(t, response.Body.String(), "receiver@")
	assert.Equal(t, events.Types(), body.Events)
	assert.Equal(t, 0, body.Queue.Depth)
	assert.Equal(t, 4, body.Queue.Capacity)
	assert.Equal(t, int64(2), body.DeadLetters)
}

// TestAdminWebhooksSummaryWithoutAReceiver is the deployment that
// configured nothing: the summary says so and still lists the events a
// receiver would get, so an operator learns what a receiver is for.
func TestAdminWebhooksSummaryWithoutAReceiver(t *testing.T) {
	handler, _ := newAdminTestController(t)
	response := httptest.NewRecorder()
	handler.Webhooks(response, httptest.NewRequest("GET", "/api/v1/admin/webhooks", nil))

	var body struct {
		Configured bool     `json:"configured"`
		Endpoints  []string `json:"endpoints"`
		Events     []string `json:"events"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.False(t, body.Configured)
	assert.Equal(t, []string{}, body.Endpoints)
	assert.Equal(t, events.Types(), body.Events)
}

// dropCount is the usage sink drop counter a test states directly.
type dropCount int64

func (d dropCount) Dropped() int64 { return int64(d) }
