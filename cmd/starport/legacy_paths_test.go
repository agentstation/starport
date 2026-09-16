package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	starportcli "github.com/agentstation/starport/internal/cli"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredInitializationRefusesImplicitLegacyMove(t *testing.T) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "STARPORT_") || strings.HasPrefix(name, "STARMAP_") {
			t.Setenv(name, "")
			require.NoError(t, os.Unsetenv(name))
		}
	}
	home := t.TempDir()
	oldConfig := filepath.Join(home, "previous-config")
	for name, path := range map[string]string{
		"HOME": home, "USERPROFILE": home, "STARPORT_CONFIG_DIR": oldConfig,
		"XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"),
		"XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_CACHE_HOME": filepath.Join(home, "cache"),
		"APPDATA": filepath.Join(home, "roaming"), "LOCALAPPDATA": filepath.Join(home, "local"),
	} {
		t.Setenv(name, path)
	}
	previous := filepath.Join(oldConfig, "data", "badger")
	require.NoError(t, os.MkdirAll(previous, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(previous, "MANIFEST"), []byte("old gateway records"), 0o600))
	paths, err := config.PlatformPaths()
	require.NoError(t, err)
	result, err := initializeConfiguredStorage(t.Context(), starportcli.InitOptions{ConfiguredStorage: true, APIKeyName: "local-admin"})
	require.ErrorIs(t, err, config.ErrLegacyPaths)
	require.Empty(t, result.APIKey)
	require.NoDirExists(t, paths.DataDir)
	require.NoFileExists(t, paths.ConfigFile)
	contents, err := os.ReadFile(filepath.Join(previous, "MANIFEST"))
	require.NoError(t, err)
	require.Equal(t, "old gateway records", string(contents))
}
