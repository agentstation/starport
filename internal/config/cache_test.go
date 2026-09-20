package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheServiceConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cache   CacheConfig
		durable string
		valid   bool
	}{
		{"default", CacheConfig{}, "", true},
		{"local", CacheConfig{Backend: "local"}, "", true},
		{"dedicated", CacheConfig{Backend: "valkey", URL: "valkeys://cache.example/3"}, "valkeys://durable.example", true},
		{"loopback", CacheConfig{Backend: "valkey", URL: "valkey://127.0.0.1:6380"}, "valkey://localhost:6379", true},
		{"same service other database", CacheConfig{Backend: "valkey", URL: "valkey://localhost:6379/3"}, "valkey://127.0.0.1/2", false},
		{"same service other credentials", CacheConfig{Backend: "valkey", URL: "valkeys://user:private-value@CACHE.example/3"}, "redis://cache.example", false},
		{"plaintext", CacheConfig{Backend: "valkey", URL: "valkey://cache.example"}, "", false},
		{"explicit plaintext", CacheConfig{Backend: "valkey", URL: "valkey://cache.example", AllowInsecure: true}, "", true},
		{"query bypass", CacheConfig{Backend: "valkey", URL: "valkeys://user:private-value@cache.example?skip_verify=true"}, "", false},
		{"local endpoint", CacheConfig{URL: "valkey://localhost"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cache.Validate(tc.durable)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-value")
			}
		})
	}
}

func TestCacheSettingsLoadAndRedact(t *testing.T) {
	cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_CACHE_BACKEND": "valkey",
		"STARPORT_CACHE_URL":     "valkeys://user:private-value@cache.example/3",
		"STARPORT_DEPLOYMENT_ID": "deployment",
	}).Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, "valkey", cfg.Cache.Backend)
	require.Equal(t, "deployment", cfg.EffectivePaths().DeploymentID)
	require.Equal(t, redactedValue, Redacted(cfg)["cache"].(map[string]any)["url"])
}

func TestDevelopmentRejectsSharedCache(t *testing.T) {
	_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_CACHE_URL": "valkeys://private-value@cache.example",
	}).LoadDevelopment(t.Context())
	require.Error(t, err)
	require.NotContains(t, OperatorError(err).Error(), "private-value")
	_, err = NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(nil).LoadDevelopment(t.Context(), func(c *Config) {
		c.Cache = CacheConfig{Backend: "valkey", URL: "valkeys://private-value@cache.example"}
	})
	require.Error(t, err)
	require.NotContains(t, OperatorError(err).Error(), "private-value")
}

func TestCacheCAFilePathAndManifest(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	cfg, err := NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_CACHE_BACKEND":      "valkey",
		"STARPORT_CACHE_URL":          "valkeys://cache.example",
		"STARPORT_DEPLOYMENT_ID":      "deployment",
		"STARPORT_CACHE_CA_FILE":      "certificates/cache.pem",
		"STARPORT_RELATIVE_PATH_BASE": "config",
	}).Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(paths.ConfigDir, "certificates", "cache.pem"), cfg.Cache.CAFile)
	manifest, err := cfg.FileManifest("test")
	require.NoError(t, err)
	found := false
	for _, entry := range manifest.Files {
		if entry.ID == "cache-ca" {
			found = true
			require.Equal(t, cfg.Cache.CAFile, entry.Location.Path)
			require.Equal(t, "available", entry.Availability)
			require.Equal(t, "deployment-controlled", entry.Policy.Access)
		}
	}
	require.True(t, found)
	for _, config := range []CacheConfig{
		{CAFile: cfg.Cache.CAFile},
		{Backend: "valkey", URL: "valkey://localhost", CAFile: cfg.Cache.CAFile},
	} {
		require.Error(t, config.Validate(""))
	}
}
