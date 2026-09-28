package sqlstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// ImportRelational copies a verified image into an empty, migrated target database.
// The caller must fence every target writer and stop schema changes before import.
// restrict must close restored permission gates in the same transaction before commit.
// This operation does not change application configuration or approve recovery.
func (db *DB) ImportRelational(ctx context.Context, source string, expected SQLiteSnapshot, scratch string, restrict func(context.Context, *sql.Conn) error) (resultErr error) {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	if restrict == nil {
		return errors.New("relational import requires a recovery restriction callback")
	}
	parent, err := productfiles.ExistingDirectory(scratch)
	if err != nil {
		return err
	}
	root, err := parent.Open()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	name := ".relational-import-" + rand.Text()
	destination := filepath.Join(scratch, name)
	result, copyErr := RestoreSQLiteSnapshot(ctx, destination, source, expected)
	if !result.Published {
		return copyErr
	}
	identity, err := root.Lstat(name)
	if err != nil {
		return errors.Join(copyErr, err)
	}
	defer func() {
		current, err := root.Lstat(name)
		if err == nil && os.SameFile(identity, current) {
			resultErr = errors.Join(resultErr, root.RemoveAll(name), productfiles.SyncDirectory(root))
		}
	}()
	if copyErr != nil {
		return copyErr
	}
	image, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.Join(destination, "starport.db"))+"?mode=ro&immutable=1&_pragma=trusted_schema(0)")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, image.Close()) }()
	if err := validateRelationalSchema(ctx, image, TypeSQLite); err != nil {
		return err
	}
	high, err := auditHighWater(ctx, image, TypeSQLite)
	if err != nil {
		return err
	}
	return db.importRelational(ctx, image, high, restrict)
}

func (db *DB) importRelational(ctx context.Context, source relationalQuery, high int64, restrict func(context.Context, *sql.Conn) error) (resultErr error) {
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
		if err := restrict(ctx, owner.conn); err != nil {
			return err
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
