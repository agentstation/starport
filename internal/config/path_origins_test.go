package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPathOriginsDistinguishFilesEnvironmentAndGoOverrides(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	filePath := filepath.Join(t.TempDir(), "from-file")
	envPath := filepath.Join(t.TempDir(), "from-environment")
	goPath := filepath.Join(t.TempDir(), "from-go")
	require.NoError(t, os.WriteFile(paths.ConfigFile, []byte("STARPORT_STORAGE_BADGER_PATH="+filePath+"\n"), 0o600))
	loader := NewLoader().WithPaths(paths).WithEnvironment(map[string]string{sqlitePathEnvironment: envPath})
	cfg, err := loader.Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, "file:"+paths.ConfigFile, cfg.EffectivePaths().Origins["badger"].Origin)
	require.Equal(t, "environment", cfg.EffectivePaths().Origins["sqlite"].Origin)
	require.Equal(t, "derived:data", cfg.EffectivePaths().Origins["files"].Origin)
	cfg, err = loader.Load(t.Context(), func(c *Config) { c.Storage.Badger.Path = goPath })
	require.NoError(t, err)
	require.Equal(t, goPath, cfg.EffectivePaths().BadgerDir)
	require.Equal(t, pathOriginGoOption, cfg.EffectivePaths().Origins["badger"].Origin)
}

func TestCatalogFileOriginIdentifiesStarmapFallback(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(map[string]string{
		"STARMAP_CATALOG_SOURCE": "file", "STARMAP_CATALOG_SOURCE_URL": filepath.Join(t.TempDir(), "catalog.json"),
	}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, "starmap-fallback:environment", cfg.EffectivePaths().Origins["source-file"].Origin)
}
