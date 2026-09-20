package config

import (
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
		{"dedicated", CacheConfig{Backend: "valkey", URL: "valkeys://cache.example/3", Namespace: "deployment"}, "valkeys://durable.example", true},
		{"loopback", CacheConfig{Backend: "valkey", URL: "valkey://127.0.0.1:6380", Namespace: "deployment"}, "valkey://localhost:6379", true},
		{"same service other database", CacheConfig{Backend: "valkey", URL: "valkey://localhost:6379/3", Namespace: "deployment"}, "valkey://127.0.0.1/2", false},
		{"same service other credentials", CacheConfig{Backend: "valkey", URL: "valkeys://user:private-value@CACHE.example/3", Namespace: "deployment"}, "redis://cache.example", false},
		{"plaintext", CacheConfig{Backend: "valkey", URL: "valkey://cache.example", Namespace: "deployment"}, "", false},
		{"explicit plaintext", CacheConfig{Backend: "valkey", URL: "valkey://cache.example", Namespace: "deployment", AllowInsecure: true}, "", true},
		{"query bypass", CacheConfig{Backend: "valkey", URL: "valkeys://user:private-value@cache.example?skip_verify=true", Namespace: "deployment"}, "", false},
		{"empty namespace", CacheConfig{Backend: "valkey", URL: "valkeys://cache.example"}, "", false},
		{"namespace glob", CacheConfig{Backend: "valkey", URL: "valkeys://cache.example", Namespace: "*"}, "", false},
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
		"STARPORT_CACHE_BACKEND":   "valkey",
		"STARPORT_CACHE_URL":       "valkeys://user:private-value@cache.example/3",
		"STARPORT_CACHE_NAMESPACE": "deployment",
	}).Load(t.Context())
	require.NoError(t, err)
	require.Equal(t, "valkey", cfg.Cache.Backend)
	require.Equal(t, "deployment", cfg.Cache.Namespace)
	require.Equal(t, redactedValue, Redacted(cfg)["cache"].(map[string]any)["url"])
}

func TestDevelopmentRejectsSharedCache(t *testing.T) {
	_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_CACHE_URL": "valkeys://private-value@cache.example",
	}).LoadDevelopment(t.Context())
	require.Error(t, err)
	require.NotContains(t, OperatorError(err).Error(), "private-value")
	_, err = NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(nil).LoadDevelopment(t.Context(), func(c *Config) {
		c.Cache = CacheConfig{Backend: "valkey", URL: "valkeys://private-value@cache.example", Namespace: "deployment"}
	})
	require.Error(t, err)
	require.NotContains(t, OperatorError(err).Error(), "private-value")
}
