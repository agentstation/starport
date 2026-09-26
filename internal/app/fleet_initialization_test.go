package app

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/recovery"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestInitializeFleetFreshStartup(t *testing.T) {
	valkeyURL, postgresURL := os.Getenv("TEST_FRESH_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL")
	if valkeyURL == "" || postgresURL == "" {
		t.Skip("UNVERIFIED: dedicated fresh Valkey and PostgreSQL are required")
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
	cfg.Providers = config.ProvidersConfig{}
	cfg = isolatedFleetConfig(t, cfg)
	admin, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schema := pgx.Identifier{"fresh_startup"}.Sanitize()
	_, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	parsed, err := url.Parse(postgresURL)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", "fresh_startup")
	parsed.RawQuery = query.Encode()
	cfg.Storage.SQL.Postgres.URL = parsed.String()
	record, err := InitializeFleet(t.Context(), cfg, recovery.FreshRequest{OperationID: "first", Evidence: "test/procedure"})
	require.NoError(t, err)
	require.Equal(t, cfg.EffectivePaths().DeploymentID, record.DeploymentID)
	kv, err := openStorage(cfg.Storage)
	require.NoError(t, err)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	accepted, err := catalog.OpenAcceptedStore(t.Context(), kv, db, record.DeploymentID)
	require.NoError(t, err)
	require.NotNil(t, accepted)
	keys, err := apikey.Open(kv)
	require.NoError(t, err)
	_, err = keys.Create(t.Context(), testAPIKey())
	require.NoError(t, err)
	require.NoError(t, kv.Close())
	require.NoError(t, db.Close())
	application, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, application)
	require.NoError(t, application.Close(t.Context()))
	_, err = InitializeFleet(t.Context(), cfg, recovery.FreshRequest{OperationID: "retry", Evidence: "test/procedure"})
	require.ErrorIs(t, err, sqlstore.ErrNotFresh)
}

func TestInitializeFleetRefusesLocalStorage(t *testing.T) {
	for _, mode := range []string{"nil", "badger", "sqlite", "mysql", "cluster"} {
		t.Run(mode, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Storage.Mode = storage.StorageTypeValkey
			cfg.Storage.SQL.Mode = sqlstore.TypePostgres
			switch mode {
			case "nil":
				cfg = nil
			case "badger":
				cfg.Storage.Mode = storage.StorageTypeBadger
			case "sqlite":
				cfg.Storage.SQL.Mode = sqlstore.TypeSQLite
			case "mysql":
				cfg.Storage.SQL.Mode = sqlstore.TypeMySQL
			case "cluster":
				cfg.Storage.Valkey.ClusterMode = true
			}
			_, err := InitializeFleet(t.Context(), cfg, recovery.FreshRequest{OperationID: "first", Evidence: "test/procedure"})
			require.ErrorContains(t, err, "requires standalone Valkey and PostgreSQL")
		})
	}
}
