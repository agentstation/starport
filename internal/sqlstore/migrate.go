package sqlstore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// The schema lives here and nowhere else. A migration is one .sql file named
// NNNN_description.sql under the dialect's directory; Migrate applies the
// dialect's files in name order, each in its own transaction, and records
// each applied name so a file runs exactly once for the life of a database.
//
// Each dialect keeps its own directory because the engines disagree on the
// margins — MySQL cannot index an unbounded TEXT column or parse ON
// CONFLICT — and a shared file that papers over that with the lowest common
// subset hides the disagreement instead of owning it. The three files with
// one name are the same logical migration; the contract tests hold every
// backend to the same resulting behavior.
//
//go:embed migrations/*/*.sql
var migrations embed.FS

// Migrate brings the store's schema to the current set of embedded
// migrations. It is safe to call on every startup: an already-applied file
// is skipped, a new one is applied, and a failure leaves the recorded state
// equal to what actually ran.
func (db *DB) Migrate(ctx context.Context) error {
	return db.migrate(ctx, migrations)
}

// migrate is Migrate over an explicit filesystem, so a test can prove the
// runner's contract without shipping a test schema in the binary.
func (db *DB) migrate(ctx context.Context, fsys fs.FS) error {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	if _, err := db.ExecContext(ctx, db.schemaMigrationsDDL()); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := migrationNames(fsys, db.dialect)
	if err != nil {
		return err
	}
	for _, name := range names {
		applied, err := db.migrationApplied(ctx, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := db.applyMigration(ctx, fsys, name); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

// schemaMigrationsDDL is the one statement the runner owns itself. MySQL
// cannot index an unbounded TEXT primary key, so it gets a bounded name.
func (db *DB) schemaMigrationsDDL() string {
	name := "name TEXT PRIMARY KEY"
	if db.dialect == TypeMySQL {
		name = "name VARCHAR(191) PRIMARY KEY"
	}
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		` + name + `,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
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

func (db *DB) migrationApplied(ctx context.Context, name string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		db.Bind(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`), name,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("read schema_migrations: %w", err)
	}
	return count > 0, nil
}

// applyMigration runs one file and records it inside the same transaction,
// so the record and the schema cannot disagree. (MySQL auto-commits DDL, so
// its migration files use IF NOT EXISTS guards to stay safe under a retry.)
func (db *DB) applyMigration(ctx context.Context, fsys fs.FS, name string) error {
	body, err := fs.ReadFile(fsys, "migrations/"+db.dialect+"/"+name)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		db.Bind(`INSERT INTO schema_migrations (name) VALUES (?)`), name); err != nil {
		return err
	}
	return tx.Commit()
}

// Bind rewrites ? placeholders into the dialect's form. PostgreSQL numbers
// its placeholders; the other engines take ? as written. A repository on
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
