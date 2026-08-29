package statuspage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func historyReaderFor(t *testing.T, targets map[catalogs.ProviderID]Target, now time.Time) *HistoryReader {
	t.Helper()
	reader, err := NewHistoryReader(Config{}, &mapSource{targets: targets})
	require.NoError(t, err)
	reader.clock = func() time.Time { return now }
	return reader
}

func TestHistoryStatuspageReadsTheIncidentLog(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/incidents.json", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incidents":[
			{"name":"Elevated errors on the API","status":"investigating","impact":"critical",
			 "started_at":"2026-08-29T09:30:00.000Z","created_at":"2026-08-29T09:31:00.000Z","resolved_at":null,
			 "shortlink":"https://stspg.io/active","components":[{"name":"API"},{"name":"  "}],
			 "incident_updates":[{"body":"<p>We are <strong>investigating</strong> elevated errors.</p>"},{"body":"older update"}]},
			{"name":"Degraded latency","status":"resolved","impact":"minor",
			 "started_at":"2026-08-10T02:00:00.000Z","created_at":"2026-08-10T02:05:00.000Z","resolved_at":"2026-08-10T04:00:00.000Z",
			 "shortlink":"https://stspg.io/resolved","components":[],"incident_updates":[]},
			{"name":"Impactless notice","status":"resolved","impact":"none",
			 "started_at":"2026-08-12T02:00:00.000Z","created_at":"","resolved_at":"2026-08-12T03:00:00.000Z",
			 "shortlink":"","components":[],"incident_updates":[]},
			{"name":"Ancient outage","status":"resolved","impact":"major",
			 "started_at":"2026-04-01T00:00:00.000Z","created_at":"","resolved_at":"2026-04-01T06:00:00.000Z",
			 "shortlink":"","components":[],"incident_updates":[]}
		]}`))
	}))
	defer server.Close()

	reader := historyReaderFor(t, map[catalogs.ProviderID]Target{
		"anthropic": {URL: server.URL + "/api/v2/summary.json"},
	}, now)

	history := reader.History(context.Background(), "anthropic")
	require.Equal(t, HistoryAvailable, history.Availability)
	require.Equal(t, now, history.FetchedAt)
	// The 90-day window keeps three of the four; newest first.
	require.Len(t, history.Incidents, 3)

	active := history.Incidents[0]
	require.Equal(t, "Elevated errors on the API", active.Title)
	require.Equal(t, IndicatorCritical, active.Indicator)
	require.Equal(t, "investigating", active.Status)
	require.Equal(t, time.Date(2026, 8, 29, 9, 30, 0, 0, time.UTC), active.StartedAt)
	require.True(t, active.ResolvedAt.IsZero())
	require.Equal(t, "https://stspg.io/active", active.URL)
	require.Equal(t, "We are investigating elevated errors.", active.Update)
	require.Equal(t, []string{"API"}, active.Components)

	// An impact of "none" stays unstated rather than asserting a severity.
	require.Equal(t, Indicator(""), history.Incidents[1].Indicator)

	resolved := history.Incidents[2]
	require.Equal(t, "Degraded latency", resolved.Title)
	require.Equal(t, IndicatorMinor, resolved.Indicator)
	require.Equal(t, time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC), resolved.ResolvedAt)
}

func TestHistoryCapsTheIncidentCount(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	var entries []string
	for index := range 30 {
		entries = append(entries, fmt.Sprintf(
			`{"name":"Incident %02d","status":"resolved","impact":"minor","started_at":"2026-08-%02dT01:00:00Z","resolved_at":"2026-08-%02dT02:00:00Z"}`,
			index, index%28+1, index%28+1))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"incidents":[%s]}`, strings.Join(entries, ","))
	}))
	defer server.Close()

	reader := historyReaderFor(t, map[catalogs.ProviderID]Target{
		"anthropic": {URL: server.URL + "/api/v2/summary.json"},
	}, now)

	history := reader.History(context.Background(), "anthropic")
	require.Equal(t, HistoryAvailable, history.Availability)
	require.Len(t, history.Incidents, maxHistoryIncidents)
	// Newest first even though the fixture is unordered.
	for index := 1; index < len(history.Incidents); index++ {
		require.False(t, history.Incidents[index].StartedAt.After(history.Incidents[index-1].StartedAt))
	}
}

func TestHistoryRSSReadsTheWholeFeed(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel>
			<item><title>API errors</title><description>Investigating elevated errors</description>
				<pubDate>Sat, 29 Aug 2026 11:00:00 +0000</pubDate><link>https://status.example.com/incident/2</link></item>
			<item><title>Maintenance resolved</title><description>The maintenance is complete and resolved.</description>
				<pubDate>Mon, 10 Aug 2026 03:00:00 +0000</pubDate><link>https://status.example.com/incident/1</link></item>
		</channel></rss>`))
	}))
	defer server.Close()

	reader := historyReaderFor(t, map[catalogs.ProviderID]Target{
		"mistral": {URL: server.URL + "/feed", Kind: catalogs.HealthAPIKindRSS},
	}, now)

	history := reader.History(context.Background(), "mistral")
	require.Equal(t, HistoryAvailable, history.Availability)
	require.Len(t, history.Incidents, 2)

	live := history.Incidents[0]
	require.Equal(t, "API errors", live.Title)
	require.Equal(t, "active", live.Status)
	require.Equal(t, IndicatorMinor, live.Indicator)
	require.Equal(t, "https://status.example.com/incident/2", live.URL)

	closed := history.Incidents[1]
	require.Equal(t, "resolved", closed.Status)
	require.Equal(t, Indicator(""), closed.Indicator)
	require.True(t, closed.ResolvedAt.IsZero())
}

func TestHistoryGoogleCloudKeepsOpenAndClosedWatchedIncidents(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"begin":"2026-08-29T08:00:00+00:00","end":"","external_desc":"Vertex AI elevated latency",
			 "status_impact":"SERVICE_DISRUPTION","uri":"incidents/abc123",
			 "most_recent_update":{"text":"Mitigation is <em>underway</em>."},
			 "affected_products":[{"id":"vertex","title":"Vertex AI"}]},
			{"begin":"2026-08-20T01:00:00+00:00","end":"2026-08-20T02:00:00+00:00","external_desc":"Vertex AI outage",
			 "status_impact":"SERVICE_OUTAGE","uri":"incidents/def456","most_recent_update":{"text":"Resolved."},
			 "affected_products":[{"id":"vertex","title":"Vertex AI"}]},
			{"begin":"2026-08-28T01:00:00+00:00","end":"","external_desc":"Unrelated product incident",
			 "status_impact":"SERVICE_OUTAGE","uri":"incidents/zzz","most_recent_update":{"text":"x"},
			 "affected_products":[{"id":"bigquery","title":"BigQuery"}]}
		]`))
	}))
	defer server.Close()

	reader := historyReaderFor(t, map[catalogs.ProviderID]Target{
		"google-vertex": {
			URL:        server.URL + "/incidents.json",
			Kind:       catalogs.HealthAPIKindGoogleCloud,
			Components: []catalogs.ProviderHealthComponent{{ID: "vertex", Name: "Vertex AI"}},
		},
	}, now)

	history := reader.History(context.Background(), "google-vertex")
	require.Equal(t, HistoryAvailable, history.Availability)
	require.Len(t, history.Incidents, 2)

	open := history.Incidents[0]
	require.Equal(t, "Vertex AI elevated latency", open.Title)
	require.Equal(t, IndicatorMajor, open.Indicator)
	require.Equal(t, "active", open.Status)
	require.Equal(t, "Mitigation is underway.", open.Update)
	require.Equal(t, []string{"Vertex AI"}, open.Components)
	require.Equal(t, server.URL+"/incidents/abc123", open.URL)

	closed := history.Incidents[1]
	require.Equal(t, IndicatorCritical, closed.Indicator)
	require.Equal(t, "resolved", closed.Status)
	require.Equal(t, time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC), closed.ResolvedAt)
}

func TestHistoryGoogleCloudWithoutComponentsIsUnreachable(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	reader := historyReaderFor(t, map[catalogs.ProviderID]Target{
		"google-vertex": {URL: "http://127.0.0.1:0/incidents.json", Kind: catalogs.HealthAPIKindGoogleCloud},
	}, now)
	history := reader.History(context.Background(), "google-vertex")
	require.Equal(t, HistoryUnreachable, history.Availability)
}

func TestHistoryHyperpingAndUnknownKindsAreUnpublished(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	reader := historyReaderFor(t, map[catalogs.ProviderID]Target{
		"deepinfra": {URL: "https://status.example.com/status.json", Kind: catalogs.HealthAPIKindHyperping},
		"future":    {URL: "https://status.example.com/api", Kind: catalogs.HealthAPIKind("next-thing")},
	}, now)
	require.Equal(t, HistoryUnpublished, reader.History(context.Background(), "deepinfra").Availability)
	require.Equal(t, HistoryUnpublished, reader.History(context.Background(), "future").Availability)
}

func TestHistoryUndeclaredProviderIsUnpublished(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	reader := historyReaderFor(t, nil, now)
	history := reader.History(context.Background(), "ollama")
	require.Equal(t, HistoryUnpublished, history.Availability)
	require.Empty(t, history.Incidents)
}

func TestHistoryAnUnansweredLogIsUnreachable(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	reader := historyReaderFor(t, map[catalogs.ProviderID]Target{
		"anthropic": {URL: server.URL + "/api/v2/summary.json"},
	}, now)
	history := reader.History(context.Background(), "anthropic")
	require.Equal(t, HistoryUnreachable, history.Availability)
}

func TestHistoryCachesAnswersUntilTheTTLPasses(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	var fetches atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		_, _ = w.Write([]byte(`{"incidents":[]}`))
	}))
	defer server.Close()

	reader := historyReaderFor(t, map[catalogs.ProviderID]Target{
		"anthropic": {URL: server.URL + "/api/v2/summary.json"},
	}, now)

	first := reader.History(context.Background(), "anthropic")
	second := reader.History(context.Background(), "anthropic")
	require.Equal(t, HistoryAvailable, first.Availability)
	require.Equal(t, first, second)
	require.Equal(t, int64(1), fetches.Load())

	reader.clock = func() time.Time { return now.Add(historyTTL + time.Second) }
	reader.History(context.Background(), "anthropic")
	require.Equal(t, int64(2), fetches.Load())
}
