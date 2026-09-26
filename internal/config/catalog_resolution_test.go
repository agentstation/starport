package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/stretchr/testify/require"
)

func TestCatalogInheritancePreservesExplicitSelections(t *testing.T) {
	for _, test := range []struct {
		name  string
		env   map[string]string
		file  string
		check func(*testing.T, CatalogConfig)
	}{
		{
			name: "inherit complete source and zero interval",
			env: map[string]string{
				"STARMAP_CATALOG_SOURCE":               "starmap",
				"STARMAP_CATALOG_SOURCE_URL":           "https://catalog.example/api/v1",
				"STARMAP_CATALOG_SOURCE_API_KEY":       "test-transport",
				"STARMAP_CATALOG_ACQUISITION_ENABLED":  "false",
				"STARMAP_CATALOG_SOURCE_POLL_INTERVAL": "0s",
			},
			check: func(t *testing.T, cfg CatalogConfig) {
				require.Equal(t, CatalogSourceStarmap, cfg.Source)
				require.Equal(t, "https://catalog.example/api/v1", cfg.SourceURL)
				require.Equal(t, "test-transport", cfg.SourceAPIKey)
				require.False(t, cfg.AcquisitionEnabled)
				require.Zero(t, cfg.SourcePollInterval)
			},
		},
		{
			name:  "product file precedes inherited environment",
			env:   map[string]string{"STARMAP_CATALOG_ACQUISITION_INTERVAL": "3m"},
			file:  "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n",
			check: func(t *testing.T, cfg CatalogConfig) { require.Equal(t, 7*time.Minute, cfg.AcquisitionInterval) },
		},
		{
			name:  "product false precedes inherited true",
			env:   map[string]string{"STARPORT_CATALOG_ACQUISITION_ENABLED": "false", "STARMAP_CATALOG_ACQUISITION_ENABLED": "true"},
			check: func(t *testing.T, cfg CatalogConfig) { require.False(t, cfg.AcquisitionEnabled) },
		},
		{
			name: "replacement source clears inherited credential",
			env: map[string]string{
				"STARPORT_CATALOG_SOURCE":        "starmap",
				"STARPORT_CATALOG_SOURCE_URL":    "https://replacement.example/api/v1",
				"STARMAP_CATALOG_SOURCE":         "starmap",
				"STARMAP_CATALOG_SOURCE_URL":     "https://previous.example/api/v1",
				"STARMAP_CATALOG_SOURCE_API_KEY": "previous-transport",
			},
			check: func(t *testing.T, cfg CatalogConfig) { require.Empty(t, cfg.SourceAPIKey) },
		},
		{
			name: "empty product credential disables fallback",
			env: map[string]string{
				"STARPORT_CATALOG_SOURCE_API_KEY": "",
				"STARMAP_CATALOG_SOURCE":          "starmap",
				"STARMAP_CATALOG_SOURCE_URL":      "https://catalog.example/api/v1",
				"STARMAP_CATALOG_SOURCE_API_KEY":  "previous-transport",
			},
			check: func(t *testing.T, cfg CatalogConfig) {
				require.Equal(t, CatalogSourceStarmap, cfg.Source)
				require.Empty(t, cfg.SourceAPIKey)
			},
		},
		{
			name:  "inherited file setting",
			file:  "STARMAP_CATALOG_ACQUISITION_INTERVAL=13m\n",
			check: func(t *testing.T, cfg CatalogConfig) { require.Equal(t, 13*time.Minute, cfg.AcquisitionInterval) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := PathsForConfigDir(t.TempDir())
			file := filepath.Join(paths.ConfigDir, "selected.env")
			require.NoError(t, os.WriteFile(file, []byte(test.file), 0600))
			cfg, err := NewLoader().WithPaths(paths).WithEnvironment(test.env).WithEnvFiles(file).Load(t.Context())
			require.NoError(t, err)
			test.check(t, cfg.Catalog)
		})
	}
}

func TestCatalogInheritanceExcludesNodeAndServerAuthority(t *testing.T) {
	environment := map[string]string{
		"STARMAP_HOME":                     "/another-product",
		"STARMAP_STATE_DIR":                "/another-product/runtime",
		"STARMAP_SCHEDULER_IDENTITY":       "another-product",
		"STARMAP_CATALOG_WORKSPACE_PATH":   "/another-product/workspace",
		"STARMAP_CATALOG_SOURCE_ALIASES":   "another-product",
		"STARMAP_CATALOG_AUTHORITY_ORIGIN": "invalid-server-declaration",
	}
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.Empty(t, cfg.Catalog.WorkspacePath)
	for _, name := range []string{catalogconfig.StateDirectory, catalogconfig.SchedulerIdentity, catalogconfig.WorkspacePath, catalogconfig.SourceAliases, catalogconfig.AuthorityOrigin} {
		require.NotContains(t, cfg.Catalog.CatalogValues(), name)
	}

	environment["STARPORT_CATALOG_AUTHORITY_ORIGIN"] = "invalid-server-declaration"
	_, err = NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
	require.ErrorContains(t, err, "configuration")
	require.NotContains(t, err.Error(), "invalid-server-declaration")

	delete(environment, "STARPORT_CATALOG_AUTHORITY_ORIGIN")
	environment["STARPORT_SCHEDULER_IDENTITY"] = "gateway-local"
	environment["STARPORT_CATALOG_SOURCE_ALIASES"] = "gateway-alias"
	cfg, err = NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvironment(environment).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, "gateway-local", cfg.Catalog.CatalogValues()[catalogconfig.SchedulerIdentity])
	require.Equal(t, "gateway-alias", cfg.Catalog.CatalogValues()[catalogconfig.SourceAliases])
}
