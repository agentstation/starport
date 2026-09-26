package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
	"github.com/stretchr/testify/require"
)

func TestStartupKeepsStarmapFilesAndServerSettingsIsolated(t *testing.T) {
	starportHome := filepath.Join(t.TempDir(), "starport")
	starmapHome := t.TempDir()
	sentinel := filepath.Join(starmapHome, "config.env")
	original := []byte("STARMAP_SERVER_PORT=9199\nSTARMAP_ADMIN_TOKEN=starmap-server-fixture\n")
	require.NoError(t, os.WriteFile(sentinel, original, 0o600))
	env := map[string]string{
		"STARPORT_SECURITY_MASTER_KEY":         strings.Repeat("k", 32),
		"STARPORT_HOME":                        starportHome,
		"STARPORT_INSTANCE_ID":                 "isolation-test",
		"STARPORT_CATALOG_SOURCE":              "embedded",
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
		"STARMAP_HOME":                         starmapHome,
		"STARMAP_CONFIG_DIR":                   starmapHome,
		"STARMAP_STATE_DIR":                    filepath.Join(starmapHome, "state"),
		"STARMAP_CATALOG_WORKSPACE_PATH":       filepath.Join(starmapHome, "workspace"),
		"STARMAP_SERVER_PORT":                  "9199",
		"STARMAP_ADMIN_TOKEN":                  "starmap-server-fixture",
	}
	// Set ambient values too: embedded library code must not rediscover them.
	for name, value := range env {
		if strings.HasPrefix(name, "STARMAP_") {
			t.Setenv(name, value)
		}
	}
	cfg, err := config.NewLoader().WithEnvironment(env).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, 9199, cfg.Server.Port)
	require.Empty(t, cfg.Catalog.WorkspacePath)
	require.Equal(t, filepath.Join(starportHome, "state", "catalog", "runtime", "isolation-test"), cfg.Catalog.StateDirectory)
	store, err := openStorage(cfg.Storage)
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	gatewayKey := testAPIKey()
	digest := sha256.Sum256([]byte("gateway-isolation-fixture"))
	gatewayKey.Hash = hex.EncodeToString(digest[:])
	_, err = keys.Create(t.Context(), gatewayKey)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	application, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.NotEmpty(t, application.catalog.Current().GenerationID())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	request.Header.Set("Authorization", "Bearer starmap-server-fixture")
	response := httptest.NewRecorder()
	application.httpServer.(*server.Server).Router().ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code, "Starmap administration cannot authenticate gateway requests")
	request.Header.Set("Authorization", "Bearer gateway-isolation-fixture")
	response = httptest.NewRecorder()
	application.httpServer.(*server.Server).Router().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "the gateway credential must still work")
	entries, err := os.ReadDir(starmapHome)
	require.NoError(t, err)
	require.Len(t, entries, 1, "Starport must not create state in the Starmap directory")
	require.Equal(t, "config.env", entries[0].Name())
	unchanged, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	state, err := os.ReadDir(cfg.Catalog.StateDirectory)
	require.NoError(t, err)
	require.NotEmpty(t, state, "Starport must persist its own embedded baseline")
}
