package revision

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func revisionReplayConfig(t *testing.T, backend string) sqlstore.Config {
	t.Helper()
	if backend == "sqlite" {
		return sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "starport.db")}}
	}
	config := sqlstore.Config{Type: backend}
	switch backend {
	case "postgres":
		address := os.Getenv("TEST_POSTGRES_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_POSTGRES_URL is not set")
		}
		config.Postgres.URL = address
	case "mysql":
		address := os.Getenv("TEST_MYSQL_DSN")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_MYSQL_DSN is not set")
		}
		config.MySQL.DSN = address
	}
	admin, err := sqlstore.Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	name := "revision_replay_" + strings.ToLower(rand.Text())
	var create, drop string
	if backend == "postgres" {
		parsed, err := url.Parse(config.Postgres.URL)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", name)
		parsed.RawQuery = query.Encode()
		config.Postgres.URL = parsed.String()
		quoted := pgx.Identifier{name}.Sanitize()
		create, drop = "CREATE SCHEMA "+quoted, "DROP SCHEMA "+quoted+" CASCADE"
	} else {
		parsed, err := mysql.ParseDSN(config.MySQL.DSN)
		require.NoError(t, err)
		parsed.DBName = name
		config.MySQL.DSN = parsed.FormatDSN()
		create, drop = "CREATE DATABASE `"+name+"`", "DROP DATABASE `"+name+"`"
	}
	_, err = admin.ExecContext(t.Context(), create)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := admin.ExecContext(ctx, drop)
		require.NoError(t, err)
	})
	return config
}
func revisionReplayDB(t *testing.T, config sqlstore.Config) *sqlstore.DB {
	t.Helper()
	db, err := sqlstore.Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	return db
}
func revisionReplayTarget(t *testing.T, backend string, before *Stamp) (*sqlstore.DB, sqlstore.SQLiteSnapshot, sqlstore.RelationalImportIdentity) {
	t.Helper()
	source := revisionReplayDB(t, revisionReplayConfig(t, "sqlite"))
	if before != nil {
		_, err := source.ExecContext(t.Context(), "INSERT INTO authorization_revision(id,epoch,sequence) VALUES(1,?,?)", before.Epoch, before.Sequence)
		require.NoError(t, err)
	}
	parent := filepath.Join(t.TempDir(), "private")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	target := revisionReplayDB(t, revisionReplayConfig(t, backend))
	scratch := filepath.Join(t.TempDir(), "scratch")
	_, err = productfiles.CreateDirectory(scratch)
	require.NoError(t, err)
	identity := sqlstore.RelationalImportIdentity{OperationID: "revision-replay", RestrictionID: "closed"}
	require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, scratch, identity, func(context.Context, *sql.Conn) error { return nil }))
	return target, snapshot.Snapshot, identity
}
