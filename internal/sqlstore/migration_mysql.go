package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
)

// ErrMigrationRecoveryRequired reports a MySQL migration with uncertain schema changes.
var ErrMigrationRecoveryRequired = errors.New("SQL migration requires reconciliation")

// migrateMySQL retains intent before DDL, which MySQL can commit independently.
// An interrupted attempt blocks automatic retry until an operator reconciles it.
func (db *DB) migrateMySQL(ctx context.Context, conn *sql.Conn, fsys fs.FS, names []string) error {
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migration_attempts (
		name VARCHAR(191) PRIMARY KEY,
		digest VARCHAR(64) NOT NULL
	)`); err != nil {
		return err
	}
	var pending string
	err := conn.QueryRowContext(ctx, "SELECT name FROM schema_migration_attempts ORDER BY name LIMIT 1").Scan(&pending)
	if err == nil {
		return fmt.Errorf("%w: %s", ErrMigrationRecoveryRequired, pending)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, name := range names {
		applied, err := db.migrationApplied(ctx, conn, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := fs.ReadFile(fsys, "migrations/"+db.dialect+"/"+name)
		if err != nil {
			return err
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migration_attempts (name, digest) VALUES (?, ?)", name, digest); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrMigrationRecoveryRequired, name, err)
		}
		// Commit the receipt and clear its intent in one transaction after DDL.
		owner := migrationOwner{conn: conn, dialect: db.dialect}
		if err := owner.transaction(ctx, func() error {
			if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations (name) VALUES (?)", name); err != nil {
				return err
			}
			_, err := conn.ExecContext(ctx, "DELETE FROM schema_migration_attempts WHERE name = ? AND digest = ?", name, digest)
			return err
		}); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrMigrationRecoveryRequired, name, err)
		}
	}
	return nil
}
