package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestCatalogConfigurationControlsRuntimeTraffic(t *testing.T) {
	for _, mode := range []string{"offline", "manual"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(upstream.Close)
			environment := map[string]string{
				"STARPORT_CATALOG_SOURCE":                "starmap",
				"STARPORT_CATALOG_SOURCE_URL":            upstream.URL,
				"STARPORT_CATALOG_SOURCE_POLL_INTERVAL":  "10ms",
				"STARPORT_CATALOG_ACQUISITION_ENABLED":   "false",
				"STARPORT_CATALOG_ACQUISITION_SOURCES":   "",
				"STARPORT_CATALOG_STARTUP_SPREAD":        "0s",
				"STARPORT_CATALOG_TRANSFER_IDLE_TIMEOUT": "1s",
				"STARPORT_CATALOG_TRANSFER_MAX_DURATION": "2s",
				"STARPORT_CATALOG_STATE_DIR":             filepath.Join(t.TempDir(), "runtime"),
			}
			if mode == "offline" {
				environment["STARPORT_CATALOG_NETWORK_MODE"] = "offline"
			} else {
				environment["STARPORT_CATALOG_SOURCE_REFRESH_MODE"] = "manual"
			}
			cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
			require.NoError(t, err)
			connected, err := runtimecatalog.OpenRuntime(t.Context(), storage.NewMockStore(), catalogSettings(cfg), nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connected.Close(t.Context())) })

			// Cover startup, polling, and stream subscription before the explicit request.
			require.Never(t, func() bool { return requests.Load() != 0 }, 100*time.Millisecond, time.Millisecond)
			_, err = connected.Refresh(t.Context())
			require.Error(t, err)
			if mode == "offline" {
				require.Zero(t, requests.Load())
			} else {
				require.Positive(t, requests.Load())
			}
		})
	}
}
