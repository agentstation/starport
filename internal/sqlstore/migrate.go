package sqlstore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// The schema lives here and nowhere else. A migration is one .sql file named
// NNNN_description.sql; Migrate applies the files in name order, each in its
// own transaction, and records each applied name so a file runs exactly once
// for the life of a database.
//
//go:embed migrations/*.sql
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
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := migrationNames(fsys)
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

// migrationNames lists the .sql files in name order. The name order is the
// application order, which is why every file carries a numeric prefix.
func migrationNames(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
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
		`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("read schema_migrations: %w", err)
	}
	return count > 0, nil
}

// applyMigration runs one file and records it inside the same transaction,
// so the record and the schema cannot disagree.
func (db *DB) applyMigration(ctx context.Context, fsys fs.FS, name string) error {
	body, err := fs.ReadFile(fsys, "migrations/"+name)
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
		`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		return err
	}
	return tx.Commit()
}
