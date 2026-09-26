package config

import "testing"

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
