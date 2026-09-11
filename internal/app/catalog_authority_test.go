package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestConfiguredAuthorityColdStartupKeepsDiagnostics(t *testing.T) {
	for _, state := range []string{"unavailable", "pending"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			t.Cleanup(cancel)
			requestStarted := make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case requestStarted <- struct{}{}:
				default:
				}
				if state == "pending" {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(upstream.Close)
			cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvironment(map[string]string{
				"STARPORT_CATALOG_SOURCE":                "starmap",
				"STARPORT_CATALOG_SOURCE_URL":            upstream.URL + "/api/v1",
				"STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "require_authority",
				"STARPORT_CATALOG_SOURCE_AUTHORITY_ID":   "enterprise",
				"STARPORT_CATALOG_SOURCE_POLICY_ID":      "production",
				"STARPORT_CATALOG_ACQUISITION_ENABLED":   "false",
				"STARPORT_CATALOG_STATE_DIR":             filepath.Join(t.TempDir(), "catalog-state"),
			}).WithEnvFiles().Load(ctx)
			require.NoError(t, err)
			store, err := storage.OpenBadger(storage.BadgerConfig{
				Path: t.TempDir(), SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20,
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			connected, err := runtimecatalog.OpenRuntime(ctx, store, catalogSettings(cfg), func(string) (string, bool) { return "", false })
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connected.Close(t.Context())) })
			require.NoError(t, ctx.Err(), "startup must not wait for an authority response")

			// Authority polling runs in the background. Diagnostics do not await its response.
			select {
			case <-requestStarted:
			case <-ctx.Done():
				t.Fatal("authority polling did not reach the configured source")
			}
			status := connected.Status()
			require.True(t, status.AuthorityRequired)
			require.True(t, status.CatalogAvailable)
			require.False(t, status.Usable)
			require.False(t, status.PermissionValid)
			require.NotNil(t, connected.ControlPlane().Current().Catalog())
			require.False(t, connected.ControlPlane().Current().AllowsNewAttempt())
		})
	}
}
