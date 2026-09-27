package app

import (
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestIncompleteFleetRecipeRefusesBeforeStorageAccess(t *testing.T) {
	for _, sqlMode := range []string{"sqlite", "postgres"} {
		t.Run(sqlMode, func(t *testing.T) {
			cfg := validProductionConfig(t)
			cfg.Storage.Mode = "valkey"
			cfg.Storage.Valkey.URL = "redis://127.0.0.1:1"
			cfg.Storage.Valkey.MaxConnections = 10
			cfg.Storage.SQL.Mode = sqlMode
			cfg.Storage.SQL.Postgres.URL = "postgres://127.0.0.1:1/recipe-test"
			cfg.Files.Backend = config.BlobBackendFilesystem
			factories := explicitTestFactories()
			opened := false
			factories.openStorage = func(storage.Config) (storage.KVStore, error) {
				opened = true
				t.Fatal("incomplete fleet recipe reached storage")
				return nil, nil
			}
			application, err := New(cfg, withRuntimeFactories(factories))
			require.Error(t, err)
			require.Nil(t, application)
			require.False(t, opened)
		})
	}
}
