package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
)

func newActivityTestRepository(t *testing.T) usage.Repository {
	t.Helper()
	store := storage.NewMockStore()
	t.Cleanup(func() { _ = store.Close() })
	repository, err := usage.Open(store, usage.Options{})
	require.NoError(t, err)
	return repository
}

func activityTestRecord(keyID, requestID, model, provider, status string, at time.Time) usage.Record {
	record := usage.Record{
		RequestID:      requestID,
		KeyID:          keyID,
		Timestamp:      at,
		Protocol:       "openrouter",
		Operation:      usage.OperationChat,
		ModelRequested: model,
		ModelUsed:      model,
		Provider:       provider,
		Status:         status,
		StatusCode:     http.StatusOK,
		Tokens:         usage.Tokens{Input: 100, Output: 50, Total: 150},
		LatencyMS:      200,
		Attempts:       1,
		Cost:           &usage.Cost{NanoUSD: 1_000_000, Currency: "USD"},
	}
	if status == usage.StatusError {
		record.StatusCode = http.StatusServiceUnavailable
		record.ErrorClass = "provider_unavailable"
		record.Cost = nil
		record.CostUnavailableReason = usage.CostReasonNoRoute
	}
	return record
}

func seedActivityRecords(t *testing.T, repository usage.Repository, records ...usage.Record) {
	t.Helper()
	for _, record := range records {
		require.NoError(t, repository.Put(context.Background(), record))
	}
}

func authenticatedActivityRequest(target, keyID string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if keyID != "" {
		request = request.WithContext(requestctx.WithAPIKeyID(request.Context(), keyID))
	}
	return request
}

func decodeActivityPage(t *testing.T, body []byte) (records []usage.Record, nextCursor string) {
	t.Helper()
	var response struct {
		Data       []usage.Record `json:"data"`
		NextCursor string         `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	return response.Data, response.NextCursor
}

func TestActivityListsOwnKeyOnly(t *testing.T) {
	repository := newActivityTestRepository(t)
	base := time.Now().Add(-time.Minute)
	seedActivityRecords(t, repository,
		activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, base),
		activityTestRecord("key-a", "req-2", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(time.Second)),
		activityTestRecord("key-b", "req-3", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(2*time.Second)),
	)
	controller := NewActivityController(repository)

	recorder := httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity", "key-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	records, _ := decodeActivityPage(t, recorder.Body.Bytes())
	require.Len(t, records, 2)
	for _, record := range records {
		assert.Equal(t, "key-a", record.KeyID)
	}

	// A request without an authenticated key is refused, never widened.
	recorder = httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity", ""))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestActivityFiltersAndPagination(t *testing.T) {
	repository := newActivityTestRepository(t)
	base := time.Now().Add(-time.Minute)
	seedActivityRecords(t, repository,
		activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, base),
		activityTestRecord("key-a", "req-2", "groq/llama-3.3-70b", "groq", usage.StatusOK, base.Add(time.Second)),
		activityTestRecord("key-a", "req-3", "openai/gpt-4o", "openai", usage.StatusError, base.Add(2*time.Second)),
	)
	controller := NewActivityController(repository)

	recorder := httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity?model=groq/llama-3.3-70b", "key-a"))
	require.Equal(t, http.StatusOK, recorder.Code)
	records, _ := decodeActivityPage(t, recorder.Body.Bytes())
	require.Len(t, records, 1)
	assert.Equal(t, "req-2", records[0].RequestID)

	recorder = httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity?status=error", "key-a"))
	require.Equal(t, http.StatusOK, recorder.Code)
	records, _ = decodeActivityPage(t, recorder.Body.Bytes())
	require.Len(t, records, 1)
	assert.Equal(t, "req-3", records[0].RequestID)

	recorder = httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity?provider=openai&status=ok", "key-a"))
	require.Equal(t, http.StatusOK, recorder.Code)
	records, _ = decodeActivityPage(t, recorder.Body.Bytes())
	require.Len(t, records, 1)
	assert.Equal(t, "req-1", records[0].RequestID)

	// Newest-first pagination walks the whole listing through cursors.
	recorder = httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity?limit=1", "key-a"))
	require.Equal(t, http.StatusOK, recorder.Code)
	records, cursor := decodeActivityPage(t, recorder.Body.Bytes())
	require.Len(t, records, 1)
	assert.Equal(t, "req-3", records[0].RequestID)
	require.NotEmpty(t, cursor)

	recorder = httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity?limit=1&cursor="+cursor, "key-a"))
	require.Equal(t, http.StatusOK, recorder.Code)
	records, cursor = decodeActivityPage(t, recorder.Body.Bytes())
	require.Len(t, records, 1)
	assert.Equal(t, "req-2", records[0].RequestID)
	require.NotEmpty(t, cursor)

	recorder = httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity?since=not-a-time", "key-a"))
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	recorder = httptest.NewRecorder()
	controller.List(recorder, authenticatedActivityRequest("/api/v1/activity?limit=nope", "key-a"))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestAdminActivityFiltersByKey(t *testing.T) {
	repository := newActivityTestRepository(t)
	base := time.Now().Add(-time.Minute)
	seedActivityRecords(t, repository,
		activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, base),
		activityTestRecord("key-b", "req-2", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(time.Second)),
	)
	controller := NewActivityController(repository)

	recorder := httptest.NewRecorder()
	controller.AdminList(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	records, _ := decodeActivityPage(t, recorder.Body.Bytes())
	require.Len(t, records, 2)

	recorder = httptest.NewRecorder()
	controller.AdminList(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity?key_id=key-b", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	records, _ = decodeActivityPage(t, recorder.Body.Bytes())
	require.Len(t, records, 1)
	assert.Equal(t, "key-b", records[0].KeyID)
}

func TestAdminMetricsReflectRecordedUsage(t *testing.T) {
	repository := newActivityTestRepository(t)
	now := time.Now()
	first := activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, now.Add(-3*time.Second))
	first.LatencyMS = 100
	second := activityTestRecord("key-b", "req-2", "groq/llama-3.3-70b", "groq", usage.StatusOK, now.Add(-2*time.Second))
	second.LatencyMS = 300
	third := activityTestRecord("key-a", "req-3", "openai/gpt-4o", "openai", usage.StatusError, now.Add(-time.Second))
	third.LatencyMS = 500
	seedActivityRecords(t, repository, first, second, third)

	identities, err := identity.Open(storage.NewMockStore())
	require.NoError(t, err)
	handler := NewAdminController(identities, newAdminTestTenants(t), repository)

	recorder := httptest.NewRecorder()
	handler.Metrics(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/metrics", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var response struct {
		Requests struct {
			Total   int64 `json:"total"`
			Success int64 `json:"success"`
			Errors  int64 `json:"errors"`
		} `json:"requests"`
		Latency struct {
			P50 int64 `json:"p50"`
			P95 int64 `json:"p95"`
			P99 int64 `json:"p99"`
		} `json:"latency"`
		Tokens struct {
			Total int64 `json:"total"`
		} `json:"tokens"`
		Spend struct {
			NanoUSD  int64  `json:"nano_usd"`
			Currency string `json:"currency"`
		} `json:"spend"`
		Providers map[string]struct {
			Requests int64 `json:"requests"`
			Errors   int64 `json:"errors"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))

	assert.Equal(t, int64(3), response.Requests.Total)
	assert.Equal(t, int64(2), response.Requests.Success)
	assert.Equal(t, int64(1), response.Requests.Errors)
	assert.Equal(t, int64(300), response.Latency.P50)
	assert.Equal(t, int64(500), response.Latency.P95)
	assert.Equal(t, int64(500), response.Latency.P99)
	assert.Equal(t, int64(450), response.Tokens.Total)
	assert.Equal(t, int64(2_000_000), response.Spend.NanoUSD)
	assert.Equal(t, "USD", response.Spend.Currency)
	require.Contains(t, response.Providers, "openai")
	assert.Equal(t, int64(2), response.Providers["openai"].Requests)
	assert.Equal(t, int64(1), response.Providers["openai"].Errors)
	require.Contains(t, response.Providers, "groq")
	assert.Equal(t, int64(1), response.Providers["groq"].Requests)
}

// TestActivityByProviderAggregates covers the per-key, per-provider rollup.
// The grouping names the provider that answered, never the credential source
// that paid, because a usage record does not carry one.
func TestActivityByProviderAggregates(t *testing.T) {
	repository := newActivityTestRepository(t)
	base := time.Now().Add(-time.Minute)
	priced := activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, base)
	unpriced := activityTestRecord("key-a", "req-2", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(time.Second))
	unpriced.Cost = nil
	unpriced.CostUnavailableReason = usage.CostReasonNoPricing
	other := activityTestRecord("key-a", "req-3", "groq/llama-3.3-70b", "groq", usage.StatusOK, base.Add(2*time.Second))
	foreign := activityTestRecord("key-b", "req-4", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(3*time.Second))
	seedActivityRecords(t, repository, priced, unpriced, other, foreign)

	controller := NewActivityController(repository)

	router := chi.NewRouter()
	router.Get("/api/v1/keys/{key_id}/usage/providers", controller.ByProvider)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/keys/key-a/usage/providers", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var response struct {
		Data []struct {
			Provider            string `json:"provider"`
			Requests            int64  `json:"requests"`
			Tokens              usage.Tokens
			SpendNanoUSD        int64 `json:"spend_nano_usd"`
			RequestsWithoutCost int64 `json:"requests_without_cost"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Data, 2)

	byProvider := map[string]int{}
	for index, entry := range response.Data {
		byProvider[entry.Provider] = index
	}
	require.Contains(t, byProvider, "openai")
	require.Contains(t, byProvider, "groq")

	openai := response.Data[byProvider["openai"]]
	assert.Equal(t, int64(2), openai.Requests)
	assert.Equal(t, int64(300), openai.Tokens.Total)
	assert.Equal(t, int64(1_000_000), openai.SpendNanoUSD)
	assert.Equal(t, int64(1), openai.RequestsWithoutCost)

	groq := response.Data[byProvider["groq"]]
	assert.Equal(t, int64(1), groq.Requests)
	assert.Equal(t, int64(1_000_000), groq.SpendNanoUSD)
}
