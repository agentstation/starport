package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
)

func TestAppWithValkey(t *testing.T) {
	valkeyURL := os.Getenv("TEST_VALKEY_URL")
	if valkeyURL == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = "valkey"
	cfg.Storage.Valkey.URL = valkeyURL
	cfg.Storage.Valkey.MaxConnections = 10
	cfg.Storage.Valkey.MinIdleConns = 1
	cfg.Storage.Valkey.DialTimeout = time.Second
	cfg.Storage.Valkey.ReadTimeout = time.Second
	cfg.Storage.Valkey.WriteTimeout = time.Second
	cfg.Cache.Enabled = true
	store, err := openStorage(cfg.Storage)
	require.NoError(t, err)
	apiKeys, err := apikey.Open(store)
	require.NoError(t, err)
	_, err = apiKeys.Create(context.Background(), testAPIKey())
	if err != nil && !errors.Is(err, apikey.ErrConflict) {
		t.Fatalf("seed Valkey API key: %v", err)
	}
	require.NoError(t, store.Close())

	factories := explicitTestFactories()
	factories.openStorage = openStorage

	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	require.NotNil(t, application.cacheManager)
	require.NoError(t, application.store.Set(context.Background(), "app:test", []byte("value")))
	value, err := application.store.Get(context.Background(), "app:test")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), value)
	require.NoError(t, application.Close(context.Background()))
}

func TestStorageModeValidation(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = "invalid"
	application, err := New(cfg, withRuntimeFactories(explicitTestFactories()))
	require.Error(t, err)
	require.Nil(t, application)
}

func TestSharedStartupDoesNotCreateLocalDatabases(t *testing.T) {
	valkeyURL, postgresURL := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL")
	if valkeyURL == "" || postgresURL == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL and TEST_POSTGRES_URL are required")
	}
	cfg := validProductionConfig(t)
	cfg.Providers = config.ProvidersConfig{}
	cfg.Storage.Mode = "valkey"
	cfg.Storage.Valkey.URL = valkeyURL
	cfg.Storage.Valkey.MaxConnections = 10
	cfg.Storage.Valkey.MinIdleConns = 1
	cfg.Storage.Valkey.DialTimeout = time.Second
	cfg.Storage.Valkey.ReadTimeout = time.Second
	cfg.Storage.Valkey.WriteTimeout = time.Second
	cfg.Storage.SQL.Mode = "postgres"
	cfg.Storage.SQL.Postgres.URL = postgresURL
	local := filepath.Join(t.TempDir(), "unused-local-databases")
	cfg.Storage.Badger.Path = filepath.Join(local, "badger")
	cfg.Storage.SQL.SQLite.Path = filepath.Join(local, "sqlite", "starport.db")
	cfg.Catalog.StateDirectory = filepath.Join(t.TempDir(), "runtime")
	seed, err := openStorage(cfg.Storage)
	require.NoError(t, err)
	keys, err := apikey.Open(seed)
	require.NoError(t, err)
	_, err = keys.Create(t.Context(), testAPIKey())
	if err != nil && !errors.Is(err, apikey.ErrConflict) {
		t.Fatalf("seed shared startup API key: %v", err)
	}
	require.NoError(t, seed.Close())
	for restart := range 2 {
		var database *sqlstore.DB
		application, err := New(cfg, func(options *buildOptions) {
			original := options.factories.openSQL
			options.factories.openSQL = func(settings config.StorageConfig) (*sqlstore.DB, error) {
				opened, err := original(settings)
				database = opened
				return opened, err
			}
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
		require.Equal(t, sqlstore.TypePostgres, database.Dialect())
		require.NoError(t, database.Ping(t.Context()))
		if restart == 0 {
			require.NoError(t, application.store.Set(t.Context(), "shared-startup-probe", []byte("durable")))
		}
		value, err := application.store.Get(t.Context(), "shared-startup-probe")
		require.NoError(t, err)
		require.Equal(t, []byte("durable"), value)
		require.NoDirExists(t, local)
		require.NoError(t, application.Close(t.Context()))
		require.NoDirExists(t, local)
	}
}
