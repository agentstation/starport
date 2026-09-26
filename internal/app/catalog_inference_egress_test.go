package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOfflineCatalogAllowsInferenceEgress(t *testing.T) {
	var acquisitionCalls atomic.Int64
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		acquisitionCalls.Add(1)
		http.Error(w, "offline catalog must not contact source", http.StatusServiceUnavailable)
	}))
	t.Cleanup(source.Close)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_CATALOG_NETWORK_MODE":         "offline",
		"STARPORT_CATALOG_SOURCE":               "starmap",
		"STARPORT_CATALOG_SOURCE_URL":           source.URL,
		"STARPORT_CATALOG_SOURCE_POLL_INTERVAL": "10ms",
		"STARPORT_CATALOG_ACQUISITION_ENABLED":  "true",
		"STARPORT_CATALOG_ACQUISITION_INTERVAL": "10ms",
		"STARPORT_CATALOG_STARTUP_SPREAD":       "0s",
	}).Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, "offline", cfg.Catalog.CatalogValues()[catalogconfig.NetworkMode])
	fixture := newPerformanceFixtureForCatalog(t, 0, &cfg.Catalog)
	require.Never(t, func() bool { return acquisitionCalls.Load() != 0 }, 100*time.Millisecond, time.Millisecond)
	for _, stream := range []bool{false, true} {
		sample := fixture.measure(t, true, stream)
		require.Positive(t, sample.ResponseBytes)
	}
	require.Equal(t, int64(2), fixture.calls.Load())
	require.Zero(t, acquisitionCalls.Load())
}
