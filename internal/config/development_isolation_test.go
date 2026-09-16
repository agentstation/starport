package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDevelopmentRejectsPersistentEnvironmentSelections(t *testing.T) {
	for _, key := range []string{
		"STARPORT_STORAGE_MODE", "STARPORT_STORAGE_BADGER_PATH", "STARPORT_STORAGE_VALKEY_URL",
		"STARPORT_STORAGE_SQL_MODE", "STARPORT_STORAGE_SQL_SQLITE_PATH", "STARPORT_STORAGE_SQL_POSTGRES_URL",
		"STARPORT_STORAGE_SQL_MYSQL_DSN", "STARPORT_FILES_BACKEND", "STARPORT_FILES_PATH",
		"STARPORT_FILES_OBJECT_STORE_BUCKET", "STARPORT_CATALOG_STATE_DIR",
	} {
		t.Run(key, func(t *testing.T) {
			paths := PathsForConfigDir(t.TempDir())
			value := filepath.Join(paths.DataDir, "selected-private-value")
			switch key {
			case "STARPORT_STORAGE_MODE":
				value = "valkey"
			case "STARPORT_STORAGE_SQL_MODE":
				value = "postgres"
			case "STARPORT_FILES_BACKEND":
				value = "objectstore"
			}
			cfg, err := NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(map[string]string{key: value}).LoadDevelopment(t.Context())
			require.Error(t, err)
			require.Nil(t, cfg)
			if err != nil {
				require.Contains(t, OperatorError(err).Error(), key)
				require.NotContains(t, OperatorError(err).Error(), "selected-private-value")
				require.Contains(t, OperatorError(err).Error(), "starport init")
			}
			require.NoDirExists(t, paths.DataDir)
		})
	}
}

func TestDevelopmentRejectsPersistentGoOverrides(t *testing.T) {
	for _, test := range []struct {
		name  string
		apply Override
	}{
		{"badger", func(c *Config) { c.Storage.Badger.Path = "/selected-private-value" }},
		{"valkey", func(c *Config) { c.Storage.Mode = "valkey"; c.Storage.Valkey.URL = "redis://selected-private-value" }},
		{"sqlite", func(c *Config) { c.Storage.SQL.SQLite.Path = "/selected-private-value" }},
		{"postgres", func(c *Config) {
			c.Storage.SQL.Mode = "postgres"
			c.Storage.SQL.Postgres.URL = "postgres://selected-private-value"
		}},
		{"mysql", func(c *Config) { c.Storage.SQL.Mode = "mysql"; c.Storage.SQL.MySQL.DSN = "selected-private-value" }},
		{"blobs", func(c *Config) { c.Files.Path = "/selected-private-value" }},
		{"objectstore", func(c *Config) {
			c.Files.Backend = "objectstore"
			c.Files.ObjectStore.Bucket = "selected-private-value"
		}},
		{"catalog", func(c *Config) { c.Catalog.StateDirectory = "/selected-private-value" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(nil).LoadDevelopment(t.Context(), test.apply)
			require.Error(t, err)
			require.Nil(t, cfg)
			if err != nil {
				require.False(t, strings.Contains(OperatorError(err).Error(), "selected-private-value"))
			}
		})
	}
}

func TestDevelopmentDoesNotReadSelectedConfigurationFiles(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	selected := filepath.Join(paths.ConfigDir, "not-a-file")
	require.NoError(t, os.Mkdir(selected, 0o700))
	cfg, err := NewLoader().WithPaths(paths).WithEnvFiles(selected).WithEnvironment(map[string]string{
		"STARPORT_CONFIG_FILE":                 selected,
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).LoadDevelopment(t.Context())
	require.NoError(t, err)
	require.True(t, cfg.Storage.RuntimeStorage().Badger.InMemory)
	require.Empty(t, cfg.EffectivePaths().BaselineDir)
	require.Empty(t, cfg.EffectivePaths().DataDir)
	require.NoDirExists(t, paths.DataDir)
}
