package app

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/catalog/recovery"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

func TestAppWithValkey(t *testing.T) {
	valkeyURL, postgresURL := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL")
	if valkeyURL == "" || postgresURL == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL and TEST_POSTGRES_URL are required")
	}
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = "valkey"
	cfg.Storage.Valkey.URL = valkeyURL
	cfg.Storage.Valkey.MaxConnections = 10
	cfg.Storage.Valkey.MinIdleConns = 1
	cfg.Storage.Valkey.DialTimeout = time.Second
	cfg.Storage.Valkey.ReadTimeout = time.Second
	cfg.Storage.Valkey.WriteTimeout = time.Second
	cfg.Storage.SQL.Mode = "postgres"
	cfg.Storage.SQL.Postgres.URL = postgresURL
	cfg.Cache.Enabled = true
	cfg = isolatedFleetConfig(t, cfg)
	store, err := openStorage(cfg.Storage)
	require.NoError(t, err)
	apiKeys, err := apikey.Open(store)
	require.NoError(t, err)
	_, err = apiKeys.Create(context.Background(), testAPIKey())
	if err != nil && !errors.Is(err, apikey.ErrConflict) {
		t.Fatalf("seed Valkey API key: %v", err)
	}
	approveTestFleet(t, cfg, store)
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
	cfg = isolatedFleetConfig(t, cfg)
	seed, err := openStorage(cfg.Storage)
	require.NoError(t, err)
	keys, err := apikey.Open(seed)
	require.NoError(t, err)
	_, err = keys.Create(t.Context(), testAPIKey())
	if err != nil && !errors.Is(err, apikey.ErrConflict) {
		t.Fatalf("seed shared startup API key: %v", err)
	}
	application, err := New(cfg)
	require.ErrorIs(t, err, recovery.ErrClosed, "startup must not approve its own backend")
	require.Nil(t, application)
	approveTestFleet(t, cfg, seed)
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
		now, healthy := application.authorization.clock()
		require.True(t, healthy)
		require.NotEqual(t, now.Round(0), now)
		require.True(t, application.admissionReady())
		_, err = application.authorization.cache.Resolve(t.Context(), authorization.Identity{Subject: testAPIKey().Hash})
		require.NoError(t, err)
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

func isolatedFleetConfig(t *testing.T, base *config.Config) *config.Config {
	t.Helper()
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{"STARPORT_DEPLOYMENT_ID": "app-test-" + rand.Text()}).
		WithEnvFiles().Load(t.Context(), func(cfg *config.Config) { *cfg = *base })
	require.NoError(t, err)
	return cfg
}

// approveTestFleet models an operator's explicit approval of an isolated test backend.
// Production startup must never create this approval.
func approveTestFleet(t *testing.T, cfg *config.Config, store storage.KVStore) {
	t.Helper()
	db, err := openSQL(cfg.Storage)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.ExecContext(context.Background(), db.Bind("DELETE FROM catalog_recovery WHERE deployment_id = ?"), cfg.EffectivePaths().DeploymentID)
		require.NoError(t, err)
		require.NoError(t, db.Close())
	})
	require.NoError(t, db.Migrate(t.Context()))
	witness, err := recovery.New(db)
	require.NoError(t, err)
	closed, err := witness.Initialize(t.Context(), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	identity, err := store.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
	require.NoError(t, err)
	_, err = witness.Approve(t.Context(), closed, identity, "isolated-app-test-backend")
	require.NoError(t, err)
	// This isolated fixture explicitly starts unused. Real initialization has its own native tests.
	_, err = db.ExecContext(t.Context(), db.Bind("UPDATE catalog_recovery SET bootstrap_allowed = 1 WHERE deployment_id = ?"), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
}
