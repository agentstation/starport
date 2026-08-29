package statuspage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

type mapSource struct {
	targets map[catalogs.ProviderID]Target
}

func (s *mapSource) HealthAPIs() map[catalogs.ProviderID]Target {
	return s.targets
}

type capturePublisher struct {
	mu     sync.Mutex
	passes [][]Observation
}

func (p *capturePublisher) PublishIncidents(observations []Observation) {
	p.mu.Lock()
	p.passes = append(p.passes, observations)
	p.mu.Unlock()
}

func (p *capturePublisher) last(t *testing.T) []Observation {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.NotEmpty(t, p.passes)
	return p.passes[len(p.passes)-1]
}

func pollOne(t *testing.T, target Target) []Observation {
	t.Helper()
	source := &mapSource{targets: map[catalogs.ProviderID]Target{"p": target}}
	publisher := &capturePublisher{}
	poller, err := New(Config{}, source, publisher)
	require.NoError(t, err)
	poller.PollOnce(context.Background())
	return publisher.last(t)
}

func TestPollOncePublishesAnsweredPagesOnly(t *testing.T) {
	statuspageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/summary.json", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"indicator":"major","description":"Elevated error rates"}}`))
	}))
	defer statuspageServer.Close()
	// A health API that stops answering must contribute nothing, not a guess.
	htmlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html><body>not found</body></html>"))
	}))
	defer htmlServer.Close()

	source := &mapSource{targets: map[catalogs.ProviderID]Target{
		"groq":   {URL: statuspageServer.URL + "/api/v2/summary.json"},
		"custom": {URL: htmlServer.URL + "/api/v2/summary.json"},
	}}
	publisher := &capturePublisher{}
	poller, err := New(Config{}, source, publisher)
	require.NoError(t, err)

	poller.PollOnce(context.Background())

	observations := publisher.last(t)
	require.Len(t, observations, 1)
	require.Equal(t, catalogs.ProviderID("groq"), observations[0].ProviderID)
	require.Equal(t, IndicatorMajor, observations[0].Indicator)
	require.Equal(t, "Elevated error rates", observations[0].Description)
	require.False(t, observations[0].CheckedAt.IsZero())
}

func TestStatuspageComponentsDecideOverTheSummary(t *testing.T) {
	// The summary says all clear while a targeted component is degraded —
	// the exact situation component targeting exists for: the summary can
	// omit a component without an attached incident.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/components.json":
			_, _ = w.Write([]byte(`{"components":[
				{"id":"chat","name":"Chat Completions","status":"partial_outage"},
				{"id":"embed","name":"Embeddings","status":"operational"},
				{"id":"other","name":"Something Else","status":"major_outage"}]}`))
		case "/api/v2/summary.json":
			_, _ = w.Write([]byte(`{"status":{"indicator":"none","description":"All Systems Operational"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	observations := pollOne(t, Target{
		URL:  server.URL + "/api/v2/summary.json",
		Kind: catalogs.HealthAPIKindStatuspage,
		Components: []catalogs.ProviderHealthComponent{
			{ID: "chat", Name: "Chat Completions"},
			{ID: "embed", Name: "Embeddings"},
		},
	})
	require.Len(t, observations, 1)
	require.Equal(t, IndicatorMajor, observations[0].Indicator)
	require.Equal(t, "Chat Completions: partial outage", observations[0].Description)
}

func TestStatuspageFallsBackToTheSummaryWhenComponentsDoNotMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/components.json":
			_, _ = w.Write([]byte(`{"components":[{"id":"renamed","name":"Renamed","status":"operational"}]}`))
		case "/api/v2/summary.json":
			_, _ = w.Write([]byte(`{"status":{"indicator":"minor","description":"Partially degraded"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	observations := pollOne(t, Target{
		URL:        server.URL + "/api/v2/summary.json",
		Components: []catalogs.ProviderHealthComponent{{ID: "gone", Name: "Gone"}},
	})
	require.Len(t, observations, 1)
	require.Equal(t, IndicatorMinor, observations[0].Indicator)
	require.Equal(t, "Partially degraded", observations[0].Description)
}

func TestHyperpingTargetsDeclaredServices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schemaVersion":1,"overallStatus":"OPERATIONAL","services":[
			{"publicId":"api","displayName":"API","status":"DEGRADED"},
			{"publicId":"website","displayName":"Website","status":"OPERATIONAL"}],
			"models":[{"publicId":"some-model","displayName":"Some Model","status":"DOWN"}]}`))
	}))
	defer server.Close()

	observations := pollOne(t, Target{
		URL:        server.URL + "/status.json",
		Kind:       catalogs.HealthAPIKindHyperping,
		Components: []catalogs.ProviderHealthComponent{{ID: "api", Name: "API"}},
	})
	require.Len(t, observations, 1)
	// A non-operational Hyperping word asserts a minor incident: real
	// evidence of impairment, stated conservatively.
	require.Equal(t, IndicatorMinor, observations[0].Indicator)
	require.Equal(t, "API: degraded", observations[0].Description)
}

func TestRSSLatestUnresolvedItemAssertsAMinorIncident(t *testing.T) {
	stamp := time.Now().UTC().Format(time.RFC1123Z)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel>
			<item><title>API Degraded Performance</title>
			<description>Status: investigating</description>
			<pubDate>%s</pubDate></item>
			</channel></rss>`, stamp)
	}))
	defer server.Close()

	observations := pollOne(t, Target{URL: server.URL + "/history.rss", Kind: catalogs.HealthAPIKindRSS})
	require.Len(t, observations, 1)
	require.Equal(t, IndicatorMinor, observations[0].Indicator)
	require.Equal(t, "API Degraded Performance", observations[0].Description)
}

func TestRSSResolvedOrStaleItemsReadAsNone(t *testing.T) {
	resolved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel>
			<item><title>API Degraded Performance</title>
			<description>Status: resolved. Service restored.</description>
			<pubDate>%s</pubDate></item>
			</channel></rss>`, time.Now().UTC().Format(time.RFC1123Z))
	}))
	defer resolved.Close()
	// An old unresolved-looking item must age out: a feed has no structured
	// resolved flag, and a final update does not always carry the word.
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel>
			<item><title>Ancient Incident</title>
			<description>Status: monitoring</description>
			<pubDate>%s</pubDate></item>
			</channel></rss>`, time.Now().UTC().Add(-48*time.Hour).Format(time.RFC1123Z))
	}))
	defer stale.Close()

	for _, url := range []string{resolved.URL, stale.URL} {
		observations := pollOne(t, Target{URL: url, Kind: catalogs.HealthAPIKindRSS})
		require.Len(t, observations, 1)
		require.Equal(t, IndicatorNone, observations[0].Indicator)
		require.Empty(t, observations[0].Description)
	}
}

func TestGoogleCloudFiltersOpenIncidentsByProduct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"end":"2026-01-01T00:00:00+00:00","status_impact":"SERVICE_OUTAGE",
			 "external_desc":"Closed incident","affected_products":[{"id":"vertex","title":"Vertex Gemini API"}]},
			{"end":"","status_impact":"SERVICE_OUTAGE",
			 "external_desc":"Unrelated product is down","affected_products":[{"id":"gke","title":"Kubernetes Engine"}]},
			{"end":"","status_impact":"SERVICE_DISRUPTION",
			 "external_desc":"Vertex Gemini API elevated latency","affected_products":[{"id":"vertex","title":"Vertex Gemini API"}]}]`))
	}))
	defer server.Close()

	observations := pollOne(t, Target{
		URL:        server.URL + "/incidents.json",
		Kind:       catalogs.HealthAPIKindGoogleCloud,
		Components: []catalogs.ProviderHealthComponent{{ID: "vertex", Name: "Vertex Gemini API"}},
	})
	require.Len(t, observations, 1)
	require.Equal(t, IndicatorMajor, observations[0].Indicator)
	require.Equal(t, "Vertex Gemini API elevated latency", observations[0].Description)
}

func TestGoogleCloudWithoutComponentsAssertsNothing(t *testing.T) {
	// Without catalog components every unrelated product's incident would
	// speak for this provider, so the reader refuses to answer at all.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"end":"","status_impact":"SERVICE_OUTAGE","external_desc":"x",
			"affected_products":[{"id":"gke","title":"Kubernetes Engine"}]}]`))
	}))
	defer server.Close()

	observations := pollOne(t, Target{URL: server.URL + "/incidents.json", Kind: catalogs.HealthAPIKindGoogleCloud})
	require.Empty(t, observations)
}

func TestPollOnceDropsAnUnknownIndicator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"indicator":"weird","description":"?"}}`))
	}))
	defer server.Close()

	observations := pollOne(t, Target{URL: server.URL})
	require.Empty(t, observations)
}

func TestPollOnceDropsAnUnknownKind(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"indicator":"major","description":"real"}}`))
	}))
	defer server.Close()

	// A kind this gateway predates is a document it cannot read; guessing
	// another convention would assert evidence it does not have.
	observations := pollOne(t, Target{URL: server.URL, Kind: catalogs.HealthAPIKind("pagerduty")})
	require.Empty(t, observations)
}

func TestPollOncePublishesAnEmptyPassWithNoPages(t *testing.T) {
	publisher := &capturePublisher{}
	poller, err := New(Config{}, &mapSource{}, publisher)
	require.NoError(t, err)

	// An empty catalog still publishes, so a projection built from an
	// earlier catalog clears instead of going stale.
	poller.PollOnce(context.Background())

	require.Len(t, publisher.passes, 1)
	require.Empty(t, publisher.passes[0])
}

func TestPollOnceSkipsPublishWhenTheContextEnds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"indicator":"none","description":"All Systems Operational"}}`))
	}))
	defer server.Close()

	source := &mapSource{targets: map[catalogs.ProviderID]Target{"p": {URL: server.URL}}}
	publisher := &capturePublisher{}
	poller, err := New(Config{RequestTimeout: 100 * time.Millisecond}, source, publisher)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	poller.PollOnce(ctx)

	// A canceled pass is incomplete evidence. Publishing it would clear
	// incidents that are still live.
	require.Empty(t, publisher.passes)
}

func TestNewRejectsMissingCollaborators(t *testing.T) {
	_, err := New(Config{}, nil, &capturePublisher{})
	require.Error(t, err)
	_, err = New(Config{}, &mapSource{}, nil)
	require.Error(t, err)
}
