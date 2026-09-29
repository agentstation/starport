package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

// ImportRelational copies a verified image into an empty, migrated target database.
// The caller must fence every target writer and stop schema changes before import.
// restrict must close restored permission gates in the same transaction before commit.
// This operation does not change application configuration or approve recovery.
func (db *DB) ImportRelational(ctx context.Context, source string, expected SQLiteSnapshot, scratch string, restrict func(context.Context, *sql.Conn) error) (resultErr error) {
	return db.importRelationalImage(ctx, source, expected, scratch, restrict, nil)
}

func (db *DB) importRelationalImage(ctx context.Context, source string, expected SQLiteSnapshot, scratch string, restrict func(context.Context, *sql.Conn) error, claim []byte) (resultErr error) {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	if restrict == nil {
		return errors.New("relational import requires a recovery restriction callback")
	}
	image, err := OpenRelationalSnapshot(ctx, source, expected, scratch)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, image.Close()) }()

	high, err := auditHighWater(ctx, image, TypeSQLite)
	if err != nil {
		return err
	}
	if marker, err := readRelationalImport(ctx, image); err != nil {
		return err
	} else if marker != "" {
		return ErrImportRestricted
	}
	return db.importRelational(ctx, image, high, restrict, claim)
}

func (db *DB) importRelational(ctx context.Context, source relationalQuery, high int64, restrict func(context.Context, *sql.Conn) error, claim []byte) (resultErr error) {
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
	if db.dialect == TypeMySQL {
		if _, err := owner.conn.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE"); err != nil {
			return err
		}
	}
	return owner.transaction(ctx, func() error {
		if db.dialect == TypePostgres {
			names := make([]string, len(relationalTables))
			for i, t := range relationalTables {
				names[i] = t.name
			}
			if _, err := owner.conn.ExecContext(ctx, "LOCK TABLE "+strings.Join(names, ",")+" IN ACCESS EXCLUSIVE MODE"); err != nil {
				return err
			}
		}
		if err := validateRelationalSchema(ctx, owner.conn, db.dialect); err != nil {
			return err
		}
		marker, err := readRelationalImport(ctx, owner.conn)
		if err != nil {
			return err
		}
		if marker != "" {
			if len(claim) > 0 && marker == string(claim) {
				return nil
			}
			return ErrImportRestricted
		}
		if err := checkRelationalEmpty(ctx, owner.conn, db.dialect); err != nil {
			return err
		}
		currentHigh, err := auditHighWater(ctx, owner.conn, db.dialect)
		if err != nil {
			return err
		}
		if db.dialect == TypeMySQL && max(high, currentHigh) == math.MaxInt64 {
			return ErrAuditCounterUnrepresentable
		}
		for _, query := range []string{"DELETE FROM schema_migrations", "DELETE FROM sqlstore_meta"} {
			if _, err := owner.conn.ExecContext(ctx, query); err != nil {
				return err
			}
		}
		if err := copyRelationalRows(ctx, source, owner.conn, db.dialect); err != nil {
			return err
		}
		// Preserve historical receipts, but never import their native authority.
		for _, name := range []string{relationalActivationCurrent, relationalReplayCurrent} {
			if _, err := owner.conn.ExecContext(ctx, db.Bind("DELETE FROM sqlstore_meta WHERE name=?"), name); err != nil {
				return err
			}
		}
		if err := restrict(ctx, owner.conn); err != nil {
			return err
		}
		if len(claim) > 0 {
			if _, err := owner.conn.ExecContext(ctx, db.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), relationalImportMarker, string(claim)); err != nil {
				return err
			}
		}
		return restoreAuditHighWater(ctx, owner.conn, owner.conn, db.dialect, max(high, currentHigh))
	})
}

func checkRelationalEmpty(ctx context.Context, conn *sql.Conn, dialect string) error {
	suffix := ""
	if dialect == TypeMySQL {
		suffix = " FOR UPDATE"
	}
	for _, table := range relationalTables {
		if table.name == migrationTable {
			continue
		}
		projection := "1"
		if table.name == metadataTable {
			projection = "name,value"
		}
		rows, err := conn.QueryContext(ctx, "SELECT "+projection+" FROM "+table.name+suffix) // #nosec G202 -- identifiers and projection come only from the compiled transfer contract.
		if err != nil {
			return err
		}
		for rows.Next() {
			if table.name != metadataTable {
				return errors.Join(fmt.Errorf("%w: %s contains records", ErrNotFresh, table.name), rows.Close())
			}
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				return errors.Join(err, rows.Close())
			}
			if name != "schema" || value != "starport" {
				return errors.Join(ErrNotFresh, rows.Close())
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	return nil
}
