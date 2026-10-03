package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
)

// TestMetricsFromSampleReportsOverheadPercentiles proves the metrics view
// carries gateway-overhead percentiles beside the latency percentiles, from
// the same record sample.
func TestMetricsFromSampleReportsOverheadPercentiles(t *testing.T) {
	now := time.Now()
	records := make([]usage.Record, 0, 10)
	// Newest-first sample with overhead 1..10 ms and latency 100..1000 ms.
	for i := 1; i <= 10; i++ {
		records = append(records, usage.Record{
			Timestamp:  now.Add(-time.Duration(i) * time.Minute),
			LatencyMS:  int64(i * 100),
			OverheadMS: int64(i),
		})
	}

	metrics := metricsFromSample(records, now)

	overhead, ok := metrics["overhead"].(map[string]any)
	require.True(t, ok, "metrics must carry an overhead percentile map")
	require.EqualValues(t, 5, overhead["p50"])
	require.EqualValues(t, 10, overhead["p95"])
	require.EqualValues(t, 10, overhead["p99"])

	latency, ok := metrics["latency"].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 500, latency["p50"])
}

// TestMetricsFromSampleOverheadEmptySample proves an empty sample reports
// zero percentiles instead of failing.
func TestMetricsFromSampleOverheadEmptySample(t *testing.T) {
	metrics := metricsFromSample(nil, time.Now())

	overhead, ok := metrics["overhead"].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 0, overhead["p50"])
	require.EqualValues(t, 0, overhead["p99"])
}

// TestMetricsNameTimingBoundaries proves that the metrics route names the
// boundary of each timing and reports both as partial, and that the sample
// says when it holds only the newest records. The console labels its
// timing surfaces from these fields alone.
func TestMetricsNameTimingBoundaries(t *testing.T) {
	read := func(t *testing.T, repository usage.Repository) map[string]any {
		t.Helper()
		apiKeys, err := apikey.Open(storage.NewMockStore())
		require.NoError(t, err)
		handler := NewAdminController(apiKeys, newAdminTestAccounts(t), repository)
		recorder := httptest.NewRecorder()
		handler.Metrics(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/metrics", nil))
		require.Equal(t, http.StatusOK, recorder.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		return body
	}
	now := time.Now()

	t.Run("each timing names a partial boundary", func(t *testing.T) {
		repository := newActivityTestRepository(t)
		record := activityTestRecord("key-a", "req-1", "openai/gpt-4o", "openai", usage.StatusOK, now.Add(-time.Second))
		record.LatencyMS, record.OverheadMS = 400, 12
		seedActivityRecords(t, repository, record)

		body := read(t, repository)
		latency, ok := body["latency"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, usage.TimingGatewayService, latency["boundary"])
		require.Equal(t, false, latency["complete"])
		overhead, ok := body["overhead"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, usage.TimingGatewayAdded, overhead["boundary"])
		require.Equal(t, false, overhead["complete"])
		sample, ok := body["sample"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, false, sample["truncated"])
	})

	t.Run("a sample over the list limit is truncated", func(t *testing.T) {
		repository := newActivityTestRepository(t)
		records := make([]usage.Record, 0, usage.MaxListLimit+1)
		for i := range usage.MaxListLimit + 1 {
			records = append(records, activityTestRecord("key-a", fmt.Sprintf("req-%d", i), "openai/gpt-4o", "openai", usage.StatusOK, now.Add(-time.Duration(i+1)*time.Millisecond)))
		}
		seedActivityRecords(t, repository, records...)

		sample, ok := read(t, repository)["sample"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, true, sample["truncated"])
		require.EqualValues(t, usage.MaxListLimit, sample["records"])
	})
}
