package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

// SnapshotRelational exports the current logical schema into a portable SQLite image.
// The caller must stop cross-store writes and retain the result in the deployment manifest.
// This method does not change the source schema or approve recovered permissions.
func (db *DB) SnapshotRelational(ctx context.Context, destination string) (SQLiteSnapshotResult, error) {
	if db == nil || db.DB == nil {
		return SQLiteSnapshotResult{}, ErrClosed
	}
	return publishSQLiteSnapshot(ctx, destination, func(root *os.Root, path string) (SQLiteSnapshot, error) {
		file, err := root.OpenFile("starport.db", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return SQLiteSnapshot{}, err
		}
		if err := file.Close(); err != nil {
			return SQLiteSnapshot{}, err
		}
		candidate, err := Open(Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: filepath.Join(path, "starport.db")}})
		if err != nil {
			return SQLiteSnapshot{}, err
		}
		if err := candidate.Migrate(ctx); err != nil {
			return SQLiteSnapshot{}, errors.Join(err, candidate.Close())
		}
		copyErr := db.exportRelational(ctx, candidate)
		if err := errors.Join(copyErr, candidate.Close()); err != nil {
			return SQLiteSnapshot{}, err
		}
		// Closing the only SQLite connection checkpoints and removes its WAL sidecars.
		return inspectSQLiteSnapshot(ctx, root, path)
	})
}

func (db *DB) exportRelational(ctx context.Context, candidate *DB) (resultErr error) {
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, owner.close()) }()
	cleanup, err := prepareMySQLTransfer(ctx, owner)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	options := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	if db.dialect == TypeSQLite {
		options.Isolation = sql.LevelSerializable
	}
	source, err := owner.conn.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = source.Rollback() }()
	if err := validateRelationalSchema(ctx, source, db.dialect); err != nil {
		return err
	}
	high, err := auditHighWater(ctx, source, db.dialect)
	if err != nil {
		return err
	}
	target, err := candidate.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = target.Rollback() }()
	if _, err := target.ExecContext(ctx, "DELETE FROM schema_migrations; DELETE FROM sqlstore_meta"); err != nil {
		return err
	}
	if err := copyRelationalRows(ctx, source, target, TypeSQLite); err != nil {
		return err
	}
	if err := restoreAuditHighWater(ctx, target, target, TypeSQLite, high); err != nil {
		return err
	}
	if err := source.Commit(); err != nil {
		return err
	}
	return target.Commit()
}
