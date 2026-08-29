package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

type mapSource struct {
	pages map[catalogs.ProviderID]string
}

func (s *mapSource) StatusPages() map[catalogs.ProviderID]string {
	return s.pages
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

func TestPollOncePublishesAnsweredPagesOnly(t *testing.T) {
	statuspageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/status.json", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"indicator":"major","description":"Elevated error rates"}}`))
	}))
	defer statuspageServer.Close()
	// A provider whose status page runs other software answers the summary
	// path with HTML and a 404. It must contribute nothing, not a guess.
	htmlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html><body>not found</body></html>"))
	}))
	defer htmlServer.Close()

	source := &mapSource{pages: map[catalogs.ProviderID]string{
		"groq":   statuspageServer.URL + "/", // a trailing slash must not double up
		"custom": htmlServer.URL,
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

func TestPollOnceDropsAnUnknownIndicator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"indicator":"weird","description":"?"}}`))
	}))
	defer server.Close()

	source := &mapSource{pages: map[catalogs.ProviderID]string{"p": server.URL}}
	publisher := &capturePublisher{}
	poller, err := New(Config{}, source, publisher)
	require.NoError(t, err)

	poller.PollOnce(context.Background())

	require.Empty(t, publisher.last(t))
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

	source := &mapSource{pages: map[catalogs.ProviderID]string{"p": server.URL}}
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
