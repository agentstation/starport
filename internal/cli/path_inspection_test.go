package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfigPathsUsesEffectivePrimaryFileWithoutCreatingState(t *testing.T) {
	paths := config.PathsForConfigDir(t.TempDir())
	selected := filepath.Join(t.TempDir(), "selected-data")
	require.NoError(t, os.WriteFile(paths.ConfigFile, []byte("STARPORT_DATA_DIR="+selected+"\nSTARPORT_SECURITY_MASTER_KEY=do-not-print-this-key-with-32-bytes\n"), 0o600))
	loader := config.NewLoader().WithEnvironment(map[string]string{"STARPORT_CONFIG_DIR": paths.ConfigDir})
	deps, stdout, _ := testDependencies()
	deps.LoadConfig = func(ctx context.Context) (*config.Config, error) { return loader.Load(ctx) }
	deps.ResolvePaths = func() (config.Paths, error) { return paths, nil }
	require.NoError(t, Run(t.Context(), []string{"starport", "config", "paths", "--json"}, deps))
	var got config.Paths
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	require.Equal(t, selected, got.DataDir)
	require.NotContains(t, stdout.String(), "do-not-print-this-key-with-32-bytes")
	require.NoDirExists(t, selected)
}

func TestConfigPathsTextIncludesRuntimeAndStorageLocations(t *testing.T) {
	paths := config.PathsForConfigDir(t.TempDir())
	loader := config.NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles()
	cfg, err := loader.Load(t.Context())
	require.NoError(t, err)
	deps, stdout, _ := testDependencies()
	deps.LoadConfig = func(context.Context) (*config.Config, error) { return cfg, nil }
	require.NoError(t, Run(t.Context(), []string{"starport", "config", "paths"}, deps))
	for _, location := range []string{paths.ConfigFile, paths.StateDir, paths.CacheDir, paths.RuntimeDir, paths.BaselineDir, paths.BadgerDir, paths.SQLiteFile, paths.FilesDir, paths.LocalTokenFile} {
		require.Contains(t, stdout.String(), location)
	}
}
