package configrevision_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/sqlstore"
)

const (
	testDeployment = "deployment-a"
	testNamespace  = "namespace-a"
)

// postgresConfig returns one isolated PostgreSQL schema or skips the test.
func postgresConfig(t *testing.T) sqlstore.Config {
	t.Helper()
	address := os.Getenv("TEST_POSTGRES_URL")
	if address == "" {
		t.Skip("UNVERIFIED: TEST_POSTGRES_URL is required")
	}
	return isolatedConfig(t, sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: address}})
}

// backendConfigs returns SQLite and each network backend whose fixture is set.
func backendConfigs(t *testing.T) map[string]sqlstore.Config {
	t.Helper()
	configs := map[string]sqlstore.Config{
		sqlstore.TypeSQLite: {Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "starport.db")}},
	}
	if address := os.Getenv("TEST_POSTGRES_URL"); address != "" {
		configs[sqlstore.TypePostgres] = isolatedConfig(t, sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: address}})
	} else {
		t.Log("UNVERIFIED: TEST_POSTGRES_URL is required for the PostgreSQL case")
	}
	if dsn := os.Getenv("TEST_MYSQL_DSN"); dsn != "" {
		configs[sqlstore.TypeMySQL] = isolatedConfig(t, sqlstore.Config{Type: sqlstore.TypeMySQL, MySQL: sqlstore.MySQLConfig{DSN: dsn}})
	} else {
		t.Log("UNVERIFIED: TEST_MYSQL_DSN is required for the MySQL case")
	}
	return configs
}

// isolatedConfig keeps the test in a schema or database that it owns.
func isolatedConfig(t *testing.T, config sqlstore.Config) sqlstore.Config {
	t.Helper()
	admin, err := sqlstore.Open(config)
	require.NoError(t, err)
	name := "starport_configrevision_" + strings.ToLower(rand.Text())
	var create, drop string
	switch config.Type {
	case sqlstore.TypePostgres:
		parsed, err := url.Parse(config.Postgres.URL)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", name)
		parsed.RawQuery = query.Encode()
		config.Postgres.URL = parsed.String()
		quoted := pgx.Identifier{name}.Sanitize()
		create, drop = "CREATE SCHEMA "+quoted, "DROP SCHEMA "+quoted+" CASCADE"
	case sqlstore.TypeMySQL:
		parsed, err := mysql.ParseDSN(config.MySQL.DSN)
		require.NoError(t, err)
		parsed.DBName = name
		config.MySQL.DSN = parsed.FormatDSN()
		create, drop = "CREATE DATABASE `"+name+"`", "DROP DATABASE `"+name+"`"
	}
	if _, err := admin.ExecContext(t.Context(), create); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, drop); err != nil {
			t.Error(err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	return config
}

// openDatabase opens and migrates one database for the test.
func openDatabase(t *testing.T, config sqlstore.Config) *sqlstore.DB {
	t.Helper()
	db, err := sqlstore.Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(t.Context()))
	return db
}

func openStore(t *testing.T, db *sqlstore.DB, sealer configrevision.Sealer) (*configrevision.Store, *audit.Repository) {
	t.Helper()
	trail, err := audit.Open(db, 0)
	require.NoError(t, err)
	store, err := configrevision.New(db, trail, sealer, testDeployment, testNamespace)
	require.NoError(t, err)
	return store, trail
}

// countRows counts the rows of the configuration and audit tables.
func countRows(t *testing.T, db *sqlstore.DB) (heads, revisions, records int) {
	t.Helper()
	for table, count := range map[string]*int{
		"deployment_configuration_head":      &heads,
		"deployment_configuration_revisions": &revisions,
		"audit_log":                          &records,
	} {
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(count))
	}
	return heads, revisions, records
}
