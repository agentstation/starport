package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationStorageSelection(t *testing.T) {
	original := StorageConfig{Mode: storageModeValkey, Valkey: ValkeyConfig{URL: "valkey://cache.example:6379", Password: "first-secret"}}
	first, err := original.MigrationStorageSelection()
	require.NoError(t, err)
	require.Len(t, first, 64)
	for _, mode := range []string{"password", "alias", "address", "cluster", "badger", "relative", "memory", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			changed := original
			switch mode {
			case "password":
				changed.Valkey.Password = "replacement-secret"
			case "alias":
				changed.Valkey.URL = "redis://cache.example:6379"
			case "address":
				changed.Valkey.URL = "valkey://other.example:6379"
			case "cluster":
				changed.Valkey.ClusterMode = true
			case "badger":
				changed.Mode = storageModeBadger
				changed.Badger.Path = filepath.Join(t.TempDir(), "badger")
			case "relative":
				changed.Mode = storageModeBadger
				changed.Badger.Path = "badger"
			case "memory":
				changed.Mode = storageModeBadger
				changed.Badger.inMemory = true
			case "invalid":
				changed.Valkey.URL = "invalid%endpoint"
			}
			next, err := changed.MigrationStorageSelection()
			switch mode {
			case "relative", "memory", "invalid":
				require.Error(t, err)
			case "password", "alias":
				require.NoError(t, err)
				require.Equal(t, first, next)
			default:
				require.NoError(t, err)
				require.NotEqual(t, first, next)
			}
		})
	}
}

func TestSavedMigrationStorageSelection(t *testing.T) {
	for _, backend := range []string{storageModeBadger, storageModeValkey} {
		t.Run(backend, func(t *testing.T) {
			cfg := &Config{Storage: StorageConfig{Mode: backend}}
			primary := map[string]string{"STARPORT_STORAGE_MODE": backend}
			key := "STARPORT_STORAGE_BADGER_PATH"
			if backend == storageModeBadger {
				cfg.Storage.Badger.Path = filepath.Join(t.TempDir(), "badger")
				primary[key] = cfg.Storage.Badger.Path
			} else {
				key = "STARPORT_STORAGE_VALKEY_URL"
				cfg.Storage.Valkey.URL = "valkey://cache.example:6379"
				primary[key] = cfg.Storage.Valkey.URL
			}
			require.NoError(t, cfg.verifySavedMigrationStorage(primary))
			delete(primary, key)
			require.Error(t, cfg.verifySavedMigrationStorage(primary), "environment-only selection must not complete migration")
			if backend == storageModeBadger {
				primary[key] = filepath.Join(t.TempDir(), "other")
			} else {
				primary[key] = "valkey://other.example:6379"
			}
			require.Error(t, cfg.verifySavedMigrationStorage(primary), "saved and effective stores must match")
		})
	}
}
