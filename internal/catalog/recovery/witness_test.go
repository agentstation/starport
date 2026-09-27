package recovery

import (
	"context"
	"crypto/rand"
	"github.com/jackc/pgx/v5"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestRecoveryWitnessTransitions(t *testing.T) {
	configs := map[string]sqlstore.Config{"sqlite": {Type: sqlstore.TypeSQLite}}
	if address := os.Getenv("TEST_POSTGRES_URL"); address != "" {
		configs["postgres"] = sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: address}}
	}
	if dsn := os.Getenv("TEST_MYSQL_DSN"); dsn != "" {
		configs["mysql"] = sqlstore.Config{Type: sqlstore.TypeMySQL, MySQL: sqlstore.MySQLConfig{DSN: dsn}}
	}
	for name, config := range configs {
		t.Run(name, func(t *testing.T) {
			if config.Type == sqlstore.TypePostgres {
				config = isolatedWitnessPostgres(t, config)
			}
			db, err := sqlstore.Open(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			require.NoError(t, db.Migrate(t.Context()))
			witness, err := New(db)
			require.NoError(t, err)
			deployment := "recovery-" + rand.Text()
			t.Cleanup(func() {
				_, err := db.ExecContext(context.Background(), db.Bind("DELETE FROM catalog_recovery WHERE deployment_id = ?"), deployment)
				require.NoError(t, err)
			})
			_, err = witness.Approved(t.Context(), deployment)
			require.ErrorIs(t, err, ErrClosed)
			closed, err := witness.Initialize(t.Context(), deployment)
			require.NoError(t, err)
			require.False(t, closed.Open)
			_, err = witness.Approved(t.Context(), deployment)
			require.ErrorIs(t, err, ErrClosed)
			_, err = witness.Approve(t.Context(), closed, "backend", "")
			require.Error(t, err)
			approved, err := witness.Approve(t.Context(), closed, "backend", "verified-history-1")
			require.NoError(t, err)
			require.True(t, approved.Open)
			actual, err := witness.Approved(t.Context(), deployment)
			require.NoError(t, err)
			require.Equal(t, approved, actual)
			_, err = witness.Approve(t.Context(), closed, "other", "verified-history-2")
			require.ErrorIs(t, err, ErrConflict)
			_, err = witness.Initialize(t.Context(), deployment)
			require.Error(t, err)
			fenced, err := witness.Close(t.Context(), approved)
			require.NoError(t, err)
			require.Equal(t, approved.Epoch+1, fenced.Epoch)
			require.False(t, fenced.Open)
			_, err = witness.Approved(t.Context(), deployment)
			require.ErrorIs(t, err, ErrClosed)
			_, err = witness.Close(t.Context(), approved)
			require.ErrorIs(t, err, ErrConflict)
			_, err = witness.Approve(t.Context(), closed, "old", "verified-history-1")
			require.ErrorIs(t, err, ErrConflict)
			replacement, err := witness.Approve(t.Context(), fenced, "replacement", "verified-history-3")
			require.NoError(t, err)
			require.Equal(t, fenced.Epoch, replacement.Epoch)
			require.Equal(t, "replacement", replacement.BackendID)
			_, err = witness.Approved(t.Context(), deployment+"-other")
			require.ErrorIs(t, err, ErrClosed)
		})
	}
}

// isolatedWitnessPostgres keeps migration tests independent of parallel catalog tests.
func isolatedWitnessPostgres(t *testing.T, config sqlstore.Config) sqlstore.Config {
	t.Helper()
	admin, err := sqlstore.Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	name := "catalog_recovery_" + strings.ToLower(rand.Text())
	quoted := pgx.Identifier{name}.Sanitize()
	_, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+quoted)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		require.NoError(t, err)
	})
	parsed, err := url.Parse(config.Postgres.URL)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", name)
	parsed.RawQuery = query.Encode()
	config.Postgres.URL = parsed.String()
	return config
}
