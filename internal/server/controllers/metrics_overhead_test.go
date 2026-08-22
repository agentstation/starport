package controllers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
