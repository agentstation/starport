package sqlstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// The contract every backend must satisfy: open from configuration alone,
// migrate to the embedded schema exactly once per file, answer a ping, and
// serve reads and writes through database/sql. These tests run against every
// configured backend; contractConfigs grows as connects arrive, so a new
// backend inherits the whole contract instead of a subset someone
// remembered.
//
// The network backends follow the Valkey precedent: they join the run when
// the environment names a server, and the run reports them skipped
// (UNVERIFIED) otherwise.
func contractConfigs(t *testing.T) map[string]Config {
	t.Helper()
	configs := map[string]Config{
		"sqlite": {
			Type:   TypeSQLite,
			SQLite: SQLiteConfig{Path: filepath.Join(t.TempDir(), "starport.db")},
		},
	}
	if url := os.Getenv("TEST_POSTGRES_URL"); url != "" {
		configs["postgres"] = Config{Type: TypePostgres, Postgres: PostgresConfig{URL: url}}
	}
	if dsn := os.Getenv("TEST_MYSQL_DSN"); dsn != "" {
		configs["mysql"] = Config{Type: TypeMySQL, MySQL: MySQLConfig{DSN: dsn}}
	}
	return configs
}

// resetBackend clears the contract's tables so a shared network server
// starts every test from the state a fresh SQLite file starts from.
func resetBackend(t *testing.T, ctx context.Context, db *DB) {
	t.Helper()
	for _, table := range []string{"sqlstore_meta", "schema_migrations", "probe"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
}

func TestContractOpenMigrateReadWrite(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(config)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer db.Close()
			resetBackend(t, ctx, db)

			if err := db.Migrate(ctx); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			// Migrate is a startup call, so a second run over an
			// already-migrated database must be a no-op, not an error.
			if err := db.Migrate(ctx); err != nil {
				t.Fatalf("second Migrate: %v", err)
			}
			if err := db.Ping(ctx); err != nil {
				t.Fatalf("Ping: %v", err)
			}

			// The baseline migration owns the store's meta table; reading
			// the marker back proves schema and data both landed.
			var value string
			err = db.QueryRowContext(ctx,
				`SELECT value FROM sqlstore_meta WHERE name = 'schema'`,
			).Scan(&value)
			if err != nil {
				t.Fatalf("read baseline row: %v", err)
			}
			if value != "starport" {
				t.Fatalf("baseline marker = %q, want starport", value)
			}

			if _, err := db.ExecContext(ctx,
				`INSERT INTO sqlstore_meta (name, value) VALUES ('probe', 'v1')`,
			); err != nil {
				t.Fatalf("write: %v", err)
			}
			err = db.QueryRowContext(ctx,
				`SELECT value FROM sqlstore_meta WHERE name = 'probe'`,
			).Scan(&value)
			if err != nil || value != "v1" {
				t.Fatalf("read back = %q, %v; want v1, nil", value, err)
			}
		})
	}
}

// A transaction either lands whole or not at all; a rollback must leave no
// trace, because repository code relies on it for multi-row invariants.
func TestContractTransactionRollback(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(config)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer db.Close()
			resetBackend(t, ctx, db)
			if err := db.Migrate(ctx); err != nil {
				t.Fatalf("Migrate: %v", err)
			}

			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("BeginTx: %v", err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO sqlstore_meta (name, value) VALUES ('doomed', 'x')`,
			); err != nil {
				t.Fatalf("tx write: %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatalf("Rollback: %v", err)
			}

			var count int
			if err := db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM sqlstore_meta WHERE name = 'doomed'`,
			).Scan(&count); err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != 0 {
				t.Fatalf("rolled-back row visible: count = %d", count)
			}
		})
	}
}

// The file is the database: a second open of the same path reads what the
// first one wrote. This is the property that separates the embedded backend
// from a cache.
func TestSQLitePersistsAcrossOpens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "starport.db")
	config := Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: path}}

	first, err := Open(config)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := first.ExecContext(ctx,
		`INSERT INTO sqlstore_meta (name, value) VALUES ('durable', 'yes')`,
	); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(config)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()
	var value string
	if err := second.QueryRowContext(ctx,
		`SELECT value FROM sqlstore_meta WHERE name = 'durable'`,
	).Scan(&value); err != nil || value != "yes" {
		t.Fatalf("read after reopen = %q, %v; want yes, nil", value, err)
	}
}

// An empty path is the development runtime: a real database with no file,
// gone with the process.
func TestSQLiteInMemory(t *testing.T) {
	ctx := context.Background()
	db, err := Open(Config{Type: TypeSQLite})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestOpenRefusesUnknownType(t *testing.T) {
	for _, kind := range []string{"", "bolt", "postgresql "} {
		if _, err := Open(Config{Type: kind}); !errors.Is(err, ErrUnknownType) {
			t.Fatalf("Open(%q) = %v, want ErrUnknownType", kind, err)
		}
	}
}

// A network type without an address is a validation refusal, not a hang.
func TestOpenRefusesConnectWithoutAddress(t *testing.T) {
	if _, err := Open(Config{Type: TypePostgres}); err == nil {
		t.Fatal("Open(postgres, no URL) reported success")
	}
	if _, err := Open(Config{Type: TypeMySQL}); err == nil {
		t.Fatal("Open(mysql, no DSN) reported success")
	}
}

// The runner's own contract, proven over a test filesystem so the shipped
// binary carries only the real schema: files apply in name order, exactly
// once each, and a file added later applies on the next run without
// re-running the earlier ones.
func TestMigrateRunsEachFileOnceInOrder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(Config{Type: TypeSQLite})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	first := fstest.MapFS{
		"migrations/sqlite/0001_a.sql": {Data: []byte(
			`CREATE TABLE probe (n INTEGER); INSERT INTO probe (n) VALUES (1);`)},
		"migrations/sqlite/0002_b.sql": {Data: []byte(
			`INSERT INTO probe (n) VALUES (2);`)},
	}
	if err := db.migrate(ctx, first); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.migrate(ctx, first); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM probe`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("rows after double migrate = %d, want 2 (each file once)", count)
	}

	second := fstest.MapFS{
		"migrations/sqlite/0001_a.sql": first["migrations/sqlite/0001_a.sql"],
		"migrations/sqlite/0002_b.sql": first["migrations/sqlite/0002_b.sql"],
		"migrations/sqlite/0003_c.sql": {Data: []byte(
			`INSERT INTO probe (n) VALUES (3);`)},
	}
	if err := db.migrate(ctx, second); err != nil {
		t.Fatalf("migrate with new file: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM probe`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("rows after third file = %d, want 3", count)
	}
}

// A migration that fails must leave no record, so a corrected build applies
// the fixed file instead of skipping it as done.
func TestMigrateFailureRecordsNothing(t *testing.T) {
	ctx := context.Background()
	db, err := Open(Config{Type: TypeSQLite})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	broken := fstest.MapFS{
		"migrations/sqlite/0001_bad.sql": {Data: []byte(`CREATE SYNTAX ERROR;`)},
	}
	if err := db.migrate(ctx, broken); err == nil {
		t.Fatal("migrate over a broken file reported success")
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed migration recorded: %d rows", count)
	}
}

// Every dialect ships the same set of migration names: three files with one
// name are one logical migration, and a dialect missing one would drift
// silently until a deployment switched backends.
func TestDialectMigrationSetsAgree(t *testing.T) {
	sqlite, err := migrationNames(migrations, TypeSQLite)
	if err != nil {
		t.Fatalf("sqlite names: %v", err)
	}
	if len(sqlite) == 0 {
		t.Fatal("no sqlite migrations embedded")
	}
	for _, dialect := range []string{TypePostgres, TypeMySQL} {
		names, err := migrationNames(migrations, dialect)
		if err != nil {
			t.Fatalf("%s names: %v", dialect, err)
		}
		if len(names) != len(sqlite) {
			t.Fatalf("%s ships %d migrations, sqlite ships %d", dialect, len(names), len(sqlite))
		}
		for i := range names {
			if names[i] != sqlite[i] {
				t.Fatalf("%s migration %d = %s, sqlite has %s", dialect, i, names[i], sqlite[i])
			}
		}
	}
}

// The audit trail is only durable if its table actually ships. A rename or a
// dropped file would pass the agreement test above while silently removing
// the trail from every fresh deployment.
func TestAuditLogMigrationShips(t *testing.T) {
	names, err := migrationNames(migrations, TypeSQLite)
	if err != nil {
		t.Fatalf("sqlite names: %v", err)
	}
	for _, name := range names {
		if name == "0006_audit_log.sql" {
			return
		}
	}
	t.Fatalf("0006_audit_log missing from embedded migrations: %v", names)
}

// bind is what lets the runner write one query for three engines.
func TestBindNumbersPostgresPlaceholders(t *testing.T) {
	pg := &DB{dialect: TypePostgres}
	if got := pg.Bind("a = ? AND b = ?"); got != "a = $1 AND b = $2" {
		t.Fatalf("postgres bind = %q", got)
	}
	lite := &DB{dialect: TypeSQLite}
	if got := lite.Bind("a = ?"); got != "a = ?" {
		t.Fatalf("sqlite bind = %q", got)
	}
}
