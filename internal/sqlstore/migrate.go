package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Each migration is an embedded NNNN_description.sql file for one dialect.
// Migrate applies files in name order and records each completed migration.
// Dialect directories express each engine's SQL requirements explicitly.
// Contract tests require the same resulting behavior from every backend.
//
//go:embed migrations/*/*.sql
var migrations embed.FS

// Migrate applies pending schema files under database migration ownership.
// SQLite and PostgreSQL commit each file with its completion record.
// MySQL retains an intent before DDL and refuses automatic retries after partial failure.
func (db *DB) Migrate(ctx context.Context) error {
	return db.migrate(ctx, migrations)
}

// CheckSchemaCurrent refuses a store that lacks an embedded migration or that
// records a migration this binary does not know. It changes nothing, so a
// write path can refuse instead of migrating.
func (db *DB) CheckSchemaCurrent(ctx context.Context) error {
	return db.checkSchemaCurrent(ctx, migrations)
}

func (db *DB) checkSchemaCurrent(ctx context.Context, fsys fs.FS) (err error) {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	names, err := migrationNames(fsys, db.dialect)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("%w: read schema history: %w", ErrSchemaBehind, err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	applied := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if !known[name] {
			return fmt.Errorf("unsupported schema migration %q", name)
		}
		applied++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if applied != len(names) {
		return fmt.Errorf("%w: %d of %d migrations applied", ErrSchemaBehind, applied, len(names))
	}
	return nil
}

// migrate is Migrate over an explicit filesystem, so a test can prove the
// runner's contract without shipping a test schema in the binary.
func (db *DB) migrate(ctx context.Context, fsys fs.FS) (err error) {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	names, err := migrationNames(fsys, db.dialect)
	if err != nil {
		return err
	}
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, owner.close()) }()
	if err := owner.transaction(ctx, func() error {
		if _, err := owner.conn.ExecContext(ctx, db.schemaMigrationsDDL()); err != nil {
			return err
		}
		return db.validateMigrationHistory(ctx, owner.conn, names)
	}); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	if db.dialect == TypeMySQL {
		return db.migrateMySQL(ctx, owner.conn, fsys, names)
	}
	for _, name := range names {
		if err := owner.transaction(ctx, func() error {
			if err := db.validateMigrationHistory(ctx, owner.conn, names); err != nil {
				return err
			}
			applied, err := db.migrationApplied(ctx, owner.conn, name)
			if err != nil || applied {
				return err
			}
			return db.applyMigration(ctx, owner.conn, fsys, name)
		}); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

// schemaMigrationsDDL is the one statement the runner owns itself. MySQL
// cannot index an unbounded TEXT primary key, so it gets a bounded name.
func (db *DB) schemaMigrationsDDL() string {
	name := "name TEXT PRIMARY KEY"
	engine := ""
	timestamp := "TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP"
	if db.dialect == TypeMySQL {
		name = "name VARCHAR(191) PRIMARY KEY"
		engine = " ENGINE=InnoDB"
		timestamp = "TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)"
	}
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		` + name + `,
		applied_at ` + timestamp + `
	)` + engine
}

// migrationNames lists the dialect's .sql files in name order. The name
// order is the application order, which is why every file carries a numeric
// prefix.
func migrationNames(fsys fs.FS, dialect string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, "migrations/"+dialect)
	if err != nil {
		return nil, fmt.Errorf("read %s migrations: %w", dialect, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (db *DB) migrationApplied(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var count int
	err := conn.QueryRowContext(ctx,
		db.Bind(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`), name,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("read schema_migrations: %w", err)
	}
	return count > 0, nil
}

// applyMigration uses the transaction and connection that own this migration.
// MySQL DDL requires explicit recovery after partial application.
func (db *DB) applyMigration(ctx context.Context, conn *sql.Conn, fsys fs.FS, name string) error {
	body, err := fs.ReadFile(fsys, "migrations/"+db.dialect+"/"+name)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx,
		db.Bind(`INSERT INTO schema_migrations (name) VALUES (?)`), name); err != nil {
		return err
	}
	return nil
}

// Bind rewrites ? placeholders into the dialect's form. PostgreSQL numbers
// its placeholders. The other engines take ? as written. A repository on
// this contract writes its statements once with ? and binds per dialect.
func (db *DB) Bind(query string) string {
	if db.dialect != TypePostgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// validateMigrationHistory refuses a binary that cannot interpret existing schema history.
func (db *DB) validateMigrationHistory(ctx context.Context, conn *sql.Conn, names []string) (err error) {
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	rows, err := conn.QueryContext(ctx, "SELECT name FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("read schema history: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if !known[name] {
			return fmt.Errorf("unsupported schema migration %q", name)
		}
	}
	return rows.Err()
}
