package revision_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/jackc/pgx/v5"
)

func revisionDatabase(t *testing.T) *sqlstore.DB {
	t.Helper()
	config := sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "authority.db")}}
	if address := os.Getenv("TEST_AUTHORIZATION_POSTGRES_URL"); address != "" {
		config = isolatedPostgres(t, address)
	}
	db, err := sqlstore.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}

func isolatedPostgres(t *testing.T, address string) sqlstore.Config {
	t.Helper()
	admin, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: address}})
	if err != nil {
		t.Fatal(err)
	}
	namespace := "authorization_" + strings.ToLower(rand.Text())
	quoted := pgx.Identifier{namespace}.Sanitize()
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", namespace)
	parsed.RawQuery = query.Encode()
	return sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: parsed.String()}}
}
