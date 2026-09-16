package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestDevLetsStarmapCreateCatalogState(t *testing.T) {
	cfg := validDevelopmentConfig(t)
	cfg.Providers = config.ProvidersConfig{}
	factoryReached := false
	development, err := NewDevelopment(t.Context(), cfg, func(options *buildOptions) {
		openCatalog := options.factories.openCatalog
		options.factories.openCatalog = func(ctx context.Context, store storage.KVStore, settings runtimecatalog.Settings, lookup runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
			factoryReached = true
			require.Equal(t, cfg.EffectivePaths().CacheDir, settings.SourceCacheDirectory)
			require.True(t, filepath.IsLocal(mustRelative(t, filepath.Dir(cfg.Files.Path), settings.SourceCacheDirectory)))
			if _, err := os.Stat(settings.StateDirectory); !errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("development must let Starmap create its catalog state directory")
			}
			return openCatalog(ctx, store, settings, lookup)
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, development.Close(context.Background())) })
	require.True(t, factoryReached)
	require.DirExists(t, cfg.Catalog.StateDirectory)
}

func TestDevelopmentExportsBaselineOnlyInScratch(t *testing.T) {
	persistent := filepath.Join(t.TempDir(), "installation")
	cfg, err := config.NewLoader().WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_HOME":                        persistent,
		"STARPORT_CATALOG_NETWORK_MODE":        "offline",
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).LoadDevelopment(t.Context())
	require.NoError(t, err)
	development, err := NewDevelopment(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, development.Close(context.Background())) })
	baseline := cfg.EffectivePaths().BaselineDir
	require.NotEmpty(t, baseline)
	require.True(t, filepath.IsLocal(mustRelative(t, development.scratchRoot, baseline)))
	manifests, err := filepath.Glob(filepath.Join(baseline, "*", "manifest.json"))
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	require.FileExists(t, filepath.Join(filepath.Dir(manifests[0]), "catalog.json"))
	require.NoDirExists(t, persistent)
	require.NoError(t, development.Close(context.Background()))
	require.NoDirExists(t, development.scratchRoot)
}

func mustRelative(t *testing.T, root, path string) string {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	require.NoError(t, err)
	return relative
}

func TestDevelopmentRefusesPersistentConfigurationBeforeStores(t *testing.T) {
	for _, test := range []struct {
		name        string
		selectStore func(*config.Config, string)
	}{
		{"badger", func(c *config.Config, p string) { c.Storage.Badger.Path = p }},
		{"sqlite", func(c *config.Config, p string) { c.Storage.SQL.SQLite.Path = p }},
		{"valkey", func(c *config.Config, _ string) {
			c.Storage.Mode = "valkey"
			c.Storage.Valkey.URL = "redis://127.0.0.1:1"
		}},
		{"postgres", func(c *config.Config, _ string) {
			c.Storage.SQL.Mode = "postgres"
			c.Storage.SQL.Postgres.URL = "postgres://127.0.0.1:1/database"
		}},
		{"mysql", func(c *config.Config, _ string) {
			c.Storage.SQL.Mode = "mysql"
			c.Storage.SQL.MySQL.DSN = "unreachable"
		}},
		{"files", func(c *config.Config, p string) { c.Files.Path = p }},
		{"objectstore", func(c *config.Config, _ string) {
			c.Files.Backend = config.BlobBackendObjectStore
			c.Files.ObjectStore.Bucket = "bucket"
			c.Files.ObjectStore.Endpoint = "http://127.0.0.1:1"
		}},
		{"catalog", func(c *config.Config, p string) { c.Catalog.StateDirectory = p }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := validDevelopmentConfig(t)
			untouched := filepath.Join(t.TempDir(), "untouched")
			test.selectStore(cfg, untouched)
			reached := false
			development, err := NewDevelopment(t.Context(), cfg, func(*buildOptions) { reached = true })
			require.ErrorIs(t, err, config.ErrDevelopmentStorage)
			require.Nil(t, development)
			require.False(t, reached)
			require.NoDirExists(t, untouched)
			require.NoFileExists(t, cfg.Security.LocalTokenPath)
		})
	}
}
