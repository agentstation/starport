package config

import (
	"testing"
	"time"

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
