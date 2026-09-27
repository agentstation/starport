package config

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestFleetRecipeRejectsReplicaLocalStores(t *testing.T) {
	for _, sqlMode := range []string{"sqlite", "postgres"} {
		t.Run(sqlMode, func(t *testing.T) {
			cfg, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
				"STARPORT_DEPLOYMENT_ID":            "recipe-probe",
				"STARPORT_STORAGE_MODE":             "valkey",
				"STARPORT_STORAGE_VALKEY_URL":       "redis://127.0.0.1:6379",
				"STARPORT_STORAGE_SQL_MODE":         sqlMode,
				"STARPORT_STORAGE_SQL_POSTGRES_URL": "postgres://localhost/starport",
				"STARPORT_FILES_BACKEND":            "filesystem",
			}).Load(t.Context())
			if err == nil {
				t.Fatalf("incomplete fleet recipe accepted: KV=%s SQL=%s files=%s", cfg.Storage.Mode, cfg.Storage.SQL.Mode, cfg.Files.Backend)
			}
		})
	}
}

func TestSharedStorageRecipeSupportedModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sql     string
		cluster string
		valid   bool
	}{
		{name: "primary", sql: "postgres", cluster: "false", valid: true},
		{name: "local SQL", sql: "sqlite", cluster: "false"},
		{name: "unqualified MySQL", sql: "mysql", cluster: "false"},
		{name: "unqualified cluster", sql: "postgres", cluster: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewLoader().WithPaths(PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
				"STARPORT_DEPLOYMENT_ID":               "recipe-probe",
				"STARPORT_STORAGE_MODE":                "valkey",
				"STARPORT_STORAGE_VALKEY_URL":          "rediss://valkey.example:6379",
				"STARPORT_STORAGE_VALKEY_CLUSTER_MODE": tc.cluster,
				"STARPORT_STORAGE_SQL_MODE":            tc.sql,
				"STARPORT_STORAGE_SQL_POSTGRES_URL":    "postgres://localhost/starport",
				"STARPORT_STORAGE_SQL_MYSQL_DSN":       "starport@tcp(localhost)/starport",
				"STARPORT_FILES_BACKEND":               "objectstore",
				"STARPORT_FILES_OBJECT_STORE_BUCKET":   "fixture-bucket",
				"STARPORT_FILES_OBJECT_STORE_REGION":   "fixture-region",
			}).Load(t.Context())
			if (err == nil) != tc.valid {
				t.Fatalf("recipe validation = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func TestDurableKVConnectionSettings(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	// Build a synthetic credential URL for redaction without a static DSN.
	sqlEndpoint := &url.URL{Scheme: "postgres", Host: "sql.example", Path: "/starport", User: url.UserPassword("operator", "sql-private-value")}
	cfg, err := NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_DEPLOYMENT_ID":               "team:west/{blue}",
		"STARPORT_STORAGE_MODE":                "valkey",
		"STARPORT_STORAGE_VALKEY_URL":          "valkeys://durable.example/2",
		"STARPORT_STORAGE_VALKEY_USERNAME":     "explicit-user",
		"STARPORT_STORAGE_VALKEY_PASSWORD":     "field-private-value",
		"STARPORT_STORAGE_VALKEY_CA_FILE":      "certificates/durable.pem",
		"STARPORT_STORAGE_VALKEY_DIAL_TIMEOUT": "2s",
		"STARPORT_STORAGE_SQL_MODE":            "postgres",
		"STARPORT_STORAGE_SQL_POSTGRES_URL":    sqlEndpoint.String(),
		"STARPORT_FILES_BACKEND":               "objectstore",
		"STARPORT_FILES_OBJECT_STORE_BUCKET":   "test-bucket",
		"STARPORT_FILES_OBJECT_STORE_REGION":   "us-east-1",
		"STARPORT_RELATIVE_PATH_BASE":          "config",
	}).Load(t.Context())
	require.NoError(t, err)
	connection := cfg.RuntimeStorage().Valkey
	require.Equal(t, "team:west/{blue}", connection.DeploymentID)
	require.Equal(t, cfg.EffectivePaths().DeploymentID, connection.DeploymentID)
	require.Equal(t, "explicit-user", connection.Username)
	require.Equal(t, "field-private-value", connection.Password)
	require.Equal(t, 2*time.Second, connection.DialTimeout)
	require.Equal(t, filepath.Join(paths.ConfigDir, "certificates", "durable.pem"), connection.CAFile)
	data, err := json.Marshal(Redacted(cfg))
	require.NoError(t, err)
	require.NotContains(t, string(data), "private-value")
	manifest, err := cfg.FileManifest("test")
	require.NoError(t, err)
	found := false
	for _, entry := range manifest.Files {
		if entry.ID == valkeyCAFileRole {
			found = true
			require.Equal(t, connection.CAFile, entry.Location.Path)
			require.Equal(t, "available", entry.Availability)
			require.Equal(t, "deployment-controlled", entry.Policy.Access)
		}
	}
	require.True(t, found)
}
