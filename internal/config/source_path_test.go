package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileCatalogSourceRequiresExplicitRelativeAnchor(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	environment := map[string]string{"STARPORT_CATALOG_SOURCE": "file", "STARPORT_CATALOG_SOURCE_URL": "catalog/input.json"}
	_, err := NewLoader().WithPaths(paths).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
	require.Error(t, err, "an unanchored relative source must not depend on the working directory")
	environment["STARPORT_RELATIVE_PATH_BASE"] = "config"
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(paths.ConfigDir, "catalog", "input.json"), cfg.Catalog.SourceURL)
	require.Equal(t, paths.ConfigDir, cfg.EffectivePaths().Origins["source-file"].Anchor)
	require.NoDirExists(t, filepath.Join(paths.ConfigDir, "catalog"))
}

func TestNetworkCatalogSourceRetainsURL(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	endpoint := "https://catalog.example.test/snapshot.json"
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(map[string]string{
		"STARPORT_CATALOG_SOURCE": CatalogSourceStarmap, "STARPORT_CATALOG_SOURCE_URL": endpoint,
	}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, endpoint, cfg.Catalog.SourceURL)
	require.NotContains(t, cfg.EffectivePaths().Origins, "source-file")
}
