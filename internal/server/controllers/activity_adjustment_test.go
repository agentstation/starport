package controllers

import (
	"encoding/csv"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestActivityReportsCorrectedBillingAndOriginalEvidence(t *testing.T) {
	repository := newActivityTestRepository(t)
	now := time.Now().UTC().Add(-time.Minute)
	original := activityTestRecord("key-a", "video", "fixture/video", "fixture", usage.StatusOK, now)
	original.Operation = usage.OperationVideos
	require.NoError(t, repository.Put(t.Context(), original))
	correction := usage.Adjustment{ID: "decision", Original: original, RecordedAt: now.Add(time.Second), Cost: usage.Cost{NanoUSD: 70, Currency: "USD"}, Tokens: 8, BillingDisposition: "administrator_usage"}
	require.NoError(t, repository.(usage.AdjustmentWriter).Adjust(t.Context(), correction))
	controller := NewActivityController(repository)
	response := httptest.NewRecorder()
	controller.List(response, authenticatedActivityRequest("/api/v1/activity", "key-a"))
	require.Equal(t, http.StatusOK, response.Code)
	records, _ := decodeActivityPage(t, response.Body.Bytes())
	require.Len(t, records, 1)
	require.EqualValues(t, 70, records[0].Cost.NanoUSD)
	require.Equal(t, original.Cost, records[0].BillingAdjustment.OriginalCost)
	require.Equal(t, original.Tokens, records[0].BillingAdjustment.OriginalTokens)
	response = httptest.NewRecorder()
	controller.List(response, authenticatedActivityRequest("/api/v1/activity", "key-b"))
	records, _ = decodeActivityPage(t, response.Body.Bytes())
	require.Empty(t, records)
	response = httptest.NewRecorder()
	controller.ActivityExport(response, authenticatedActivityRequest("/api/v1/activity/export", "key-a"))
	require.Equal(t, http.StatusOK, response.Code)
	var exported usage.Record
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &exported))
	require.Equal(t, correction.ID, exported.BillingAdjustment.ID)
	require.Equal(t, original.Cost, exported.BillingAdjustment.OriginalCost)
	response = httptest.NewRecorder()
	controller.ActivityExport(response, authenticatedActivityRequest("/api/v1/activity/export?format=csv", "key-a"))
	require.Equal(t, http.StatusOK, response.Code)
	rows, err := csv.NewReader(response.Body).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	columns := map[string]int{}
	for i, name := range rows[0] {
		columns[name] = i
	}
	require.Equal(t, "70", rows[1][columns["cost_nano_usd"]])
	require.Equal(t, "1000000", rows[1][columns["billing_original_cost_nano_usd"]])
	require.Equal(t, "150", rows[1][columns["billing_original_tokens_total"]])
	require.Equal(t, "decision", rows[1][columns["billing_adjustment_id"]])
	require.Equal(t, "administrator_usage", rows[1][columns["billing_disposition"]])
	require.NotEmpty(t, rows[1][columns["billing_adjusted_at"]])
}
