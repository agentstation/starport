package controllers

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/usage"
)

func TestActivityExportStreamsNDJSONMatchingStoredRecords(t *testing.T) {
	repository := newActivityTestRepository(t)
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seedActivityRecords(t, repository,
		activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, base),
		activityTestRecord("key-a", "req-2", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(time.Second)),
		activityTestRecord("key-b", "req-3", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(2*time.Second)),
	)
	controller := NewActivityController(repository)

	recorder := httptest.NewRecorder()
	controller.ActivityExport(recorder, authenticatedActivityRequest("/api/v1/activity/export", "key-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, usage.NDJSONContentType, recorder.Header().Get("Content-Type"))

	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	require.Len(t, lines, 2, "the export must scope to the authenticated key")
	var exported []usage.Record
	for _, line := range lines {
		var record usage.Record
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		exported = append(exported, record)
	}
	// Records list newest-first; both must belong to the key and carry the
	// stored token counts.
	assert.Equal(t, "req-2", exported[0].RequestID)
	assert.Equal(t, "req-1", exported[1].RequestID)
	for _, record := range exported {
		assert.Equal(t, "key-a", record.KeyID)
		assert.Equal(t, int64(150), record.Tokens.Total)
		require.NotNil(t, record.Cost)
		assert.Equal(t, int64(1_000_000), record.Cost.NanoUSD)
	}
}

func TestActivityExportServesCSV(t *testing.T) {
	repository := newActivityTestRepository(t)
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seedActivityRecords(t, repository,
		activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, base),
	)
	controller := NewActivityController(repository)

	recorder := httptest.NewRecorder()
	controller.ActivityExport(recorder, authenticatedActivityRequest("/api/v1/activity/export?format=csv", "key-a"))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "text/csv", recorder.Header().Get("Content-Type"))

	rows, err := csv.NewReader(recorder.Body).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2, "one header row and one record row")
	assert.Equal(t, activityExportCSVHeader, rows[0])
	row := rows[1]
	assert.Equal(t, "req-1", row[0])
	assert.Equal(t, "key-a", row[2])
	assert.Equal(t, "openai/gpt-4o", row[6])
	assert.Equal(t, "1000000", row[17])
}

func TestActivityExportRefusesAnUnknownFormat(t *testing.T) {
	controller := NewActivityController(newActivityTestRepository(t))

	recorder := httptest.NewRecorder()
	controller.ActivityExport(recorder, authenticatedActivityRequest("/api/v1/activity/export?format=xml", "key-a"))

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestActivityExportRequiresAuthentication(t *testing.T) {
	controller := NewActivityController(newActivityTestRepository(t))

	recorder := httptest.NewRecorder()
	controller.ActivityExport(recorder, authenticatedActivityRequest("/api/v1/activity/export", ""))

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestAdminExportSpansKeysAndNarrowsToOne(t *testing.T) {
	repository := newActivityTestRepository(t)
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seedActivityRecords(t, repository,
		activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, base),
		activityTestRecord("key-a", "req-2", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(time.Second)),
		activityTestRecord("key-b", "req-3", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(2*time.Second)),
	)
	controller := NewActivityController(repository)

	recorder := httptest.NewRecorder()
	controller.AdminExport(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity/export", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, usage.NDJSONContentType, recorder.Header().Get("Content-Type"))
	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	require.Len(t, lines, 3, "the admin export spans every key")

	recorder = httptest.NewRecorder()
	controller.AdminExport(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/activity/export?key_id=key-b&format=csv", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	rows, err := csv.NewReader(recorder.Body).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2, "one header row and the one record of key-b")
	assert.Equal(t, "req-3", rows[1][0])
}

func TestActivityExportCarriesCacheAndGuardrailColumns(t *testing.T) {
	repository := newActivityTestRepository(t)
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	refused := activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusError, base)
	refused.GuardrailVerdict = "refuse"
	refused.GuardrailCheck = "prompt-injection"
	cached := activityTestRecord("key-a", "req-2", "openai/gpt-4o", "openai", usage.StatusOK, base.Add(time.Second))
	cached.CacheStatus = "HIT"
	cached.CacheSemantic = true
	cached.CacheSimilarity = 0.9375
	seedActivityRecords(t, repository, refused, cached)
	controller := NewActivityController(repository)

	recorder := httptest.NewRecorder()
	controller.ActivityExport(recorder, authenticatedActivityRequest("/api/v1/activity/export?format=csv", "key-a"))
	require.Equal(t, http.StatusOK, recorder.Code)
	rows, err := csv.NewReader(recorder.Body).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3)

	column := map[string]int{}
	for index, name := range rows[0] {
		column[name] = index
	}
	// Newest first: the cached record, then the refused one.
	assert.Equal(t, "HIT", rows[1][column["cache_status"]])
	assert.Equal(t, "true", rows[1][column["cache_semantic"]])
	assert.Equal(t, "0.9375", rows[1][column["cache_similarity"]])
	assert.Equal(t, "", rows[1][column["guardrail_verdict"]])
	assert.Equal(t, "", rows[2][column["cache_similarity"]])
	assert.Equal(t, "refuse", rows[2][column["guardrail_verdict"]])
	assert.Equal(t, "prompt-injection", rows[2][column["guardrail_check"]])

	// The guardrail filter narrows the NDJSON export the same way it
	// narrows the listing.
	recorder = httptest.NewRecorder()
	controller.ActivityExport(recorder, authenticatedActivityRequest("/api/v1/activity/export?guardrail=refuse", "key-a"))
	require.Equal(t, http.StatusOK, recorder.Code)
	var record usage.Record
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(recorder.Body.String())), &record))
	assert.Equal(t, "req-1", record.RequestID)
}
