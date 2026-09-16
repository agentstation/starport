package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestImplicitDataRootsRefuseLegacyStateBeforeGatewayStores(t *testing.T) {
	home := t.TempDir()
	for name, path := range map[string]string{
		"HOME": home, "USERPROFILE": home,
		"XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"),
		"XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_CACHE_HOME": filepath.Join(home, "cache"),
		"APPDATA": filepath.Join(home, "roaming"), "LOCALAPPDATA": filepath.Join(home, "local"),
	} {
		t.Setenv(name, path)
	}
	oldConfig := filepath.Join(home, "previous-config")
	oldStore := filepath.Join(oldConfig, "data", "badger")
	require.NoError(t, os.MkdirAll(oldStore, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(oldStore, "MANIFEST"), []byte("retained old database"), 0o600))
	cfg, err := config.NewLoader().WithEnvironment(map[string]string{
		"STARPORT_CONFIG_DIR": oldConfig, "STARPORT_SECURITY_MASTER_KEY": strings.Repeat("m", 32),
		"STARPORT_CATALOG_NETWORK_MODE": "offline", "STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	application, err := New(cfg)
	if application != nil {
		require.NoError(t, application.Close(t.Context()))
	}
	require.ErrorContains(t, err, "legacy")
	require.NoDirExists(t, cfg.Storage.Badger.Path)
	require.NoFileExists(t, cfg.Storage.SQL.SQLite.Path)
	require.NoDirExists(t, cfg.EffectivePaths().RuntimeDir)
	contents, err := os.ReadFile(filepath.Join(oldStore, "MANIFEST"))
	require.NoError(t, err)
	require.Equal(t, "retained old database", string(contents))
}
