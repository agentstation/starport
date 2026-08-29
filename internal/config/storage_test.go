package config

import (
	"testing"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

func TestRuntimeStorageProjectsAdapterSettings(t *testing.T) {
	tests := []struct {
		name  string
		input StorageConfig
		check func(*testing.T, storage.Config)
	}{
		{
			name: "Badger",
			input: StorageConfig{Mode: "badger", Badger: BadgerConfig{
				Path: "/data", SyncWrites: true, Compression: "none",
			}},
			check: func(t *testing.T, got storage.Config) {
				t.Helper()
				if got.Type != storage.StorageTypeBadger || got.Badger.Path != "/data" ||
					!got.Badger.SyncWrites || got.Badger.Compression {
					t.Errorf("Badger runtime configuration = %#v", got)
				}
			},
		},
		{
			name: "Valkey",
			input: StorageConfig{Mode: "valkey", Valkey: ValkeyConfig{
				URL: "rediss://valkey.example", Password: "secret", MinIdleConns: 2,
				ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, ClusterMode: true,
			}},
			check: func(t *testing.T, got storage.Config) {
				t.Helper()
				if got.Type != storage.StorageTypeValkey || got.Valkey.URL != "rediss://valkey.example" ||
					got.Valkey.Password != "secret" || got.Valkey.MinIdleConns != 2 ||
					got.Valkey.ReadTimeout != time.Second || got.Valkey.WriteTimeout != 2*time.Second ||
					!got.Valkey.ClusterMode {
					t.Errorf("Valkey runtime configuration = %#v", got)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.check(t, test.input.RuntimeStorage())
		})
	}
}

func TestRuntimeSQLProjectsStoreSettings(t *testing.T) {
	input := StorageConfig{SQL: SQLConfig{
		Mode:     "sqlite",
		SQLite:   SQLiteConfig{Path: "/data/sqlite/starport.db"},
		Postgres: SQLPostgresConfig{URL: "postgres://db.example/starport"},
		MySQL:    SQLMySQLConfig{DSN: "starport@tcp(db.example:3306)/starport"},
	}}
	got := input.RuntimeSQL()
	if got.Type != sqlstore.TypeSQLite || got.SQLite.Path != "/data/sqlite/starport.db" ||
		got.Postgres.URL != "postgres://db.example/starport" ||
		got.MySQL.DSN != "starport@tcp(db.example:3306)/starport" {
		t.Errorf("SQL runtime configuration = %#v", got)
	}
}

// A network mode without an address is a configuration error the operator
// sees at validation, not a hang at open.
func TestSQLConfigValidatesConnectModes(t *testing.T) {
	tests := []struct {
		name    string
		input   SQLConfig
		wantErr bool
	}{
		{name: "sqlite", input: SQLConfig{Mode: "sqlite"}},
		{name: "postgres with url", input: SQLConfig{
			Mode: "postgres", Postgres: SQLPostgresConfig{URL: "postgres://db.example/starport"},
		}},
		{name: "postgres without url", input: SQLConfig{Mode: "postgres"}, wantErr: true},
		{name: "mysql with dsn", input: SQLConfig{
			Mode: "mysql", MySQL: SQLMySQLConfig{DSN: "starport@tcp(db.example)/starport"},
		}},
		{name: "mysql without dsn", input: SQLConfig{Mode: "mysql"}, wantErr: true},
		{name: "unknown", input: SQLConfig{Mode: "bolt"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.input.Validate()
			if (err != nil) != test.wantErr {
				t.Errorf("Validate() = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

// The development runtime must reset the relational store with the key-value
// one: an in-memory SQLite database (empty path) that validates, so a
// development gateway leaves no file behind.
func TestConfigureDevelopmentRuntimeSelectsInMemorySQL(t *testing.T) {
	cfg := &Config{}
	cfg.ConfigureDevelopmentRuntime()
	if err := cfg.Storage.SQL.Validate(); err != nil {
		t.Fatalf("development SQL settings invalid: %v", err)
	}
	if cfg.Storage.SQL.SQLite.Path != "" {
		t.Errorf("development SQLite path = %q, want empty (in-memory)", cfg.Storage.SQL.SQLite.Path)
	}
}
