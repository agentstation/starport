package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrNotFresh refuses initialization over existing or uncertain application state.
var ErrNotFresh = errors.New("storage is not fresh; use the migration or recovery procedure")

// CheckFresh refuses existing application records before schema migration.
func (db *DB) CheckFresh(ctx context.Context) error {
	return db.WithFreshSchema(ctx, func(*sql.Tx) error { return nil })
}

// WithFreshSchema holds application tables against writes while initialize runs.
// The caller must stop all application processes and schema changes beforehand.
// Only migration metadata may exist. A failed callback rolls back SQL changes.
func (db *DB) WithFreshSchema(ctx context.Context, initialize func(*sql.Tx) error) error {
	if db == nil || db.DB == nil || db.Dialect() != TypePostgres || initialize == nil {
		return errors.New("fresh shared initialization requires PostgreSQL and an initializer")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT n.nspname, c.relname, c.relkind::text, c.relrowsecurity
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p', 'f', 'v', 'm') ORDER BY c.relname`)
	if err != nil {
		return err
	}
	type table struct{ name, quoted string }
	var tables []table
	for rows.Next() {
		var schema, name, kind string
		var rowSecurity bool
		if err := rows.Scan(&schema, &name, &kind, &rowSecurity); err != nil {
			_ = rows.Close()
			return err
		}
		if kind != "r" || rowSecurity {
			_ = rows.Close()
			return ErrNotFresh
		}
		tables = append(tables, table{name: name, quoted: pgx.Identifier{schema, name}.Sanitize()})
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		if err := initialize(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.quoted
	}
	// Identifiers come from PostgreSQL and use pgx identifier quoting.
	if _, err := tx.ExecContext(ctx, "LOCK TABLE "+strings.Join(names, ",")+" IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return err
	}
	for _, t := range tables {
		switch t.name {
		case migrationTable:
			continue
		case metadataTable:
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+t.quoted+" WHERE name <> 'schema' OR value <> 'starport'").Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return ErrNotFresh
			}
		default:
			var populated bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM "+t.quoted+")").Scan(&populated); err != nil {
				return err
			}
			if populated {
				return fmt.Errorf("%w: table %s contains records", ErrNotFresh, t.name)
			}
		}
	}
	if err := initialize(tx); err != nil {
		return err
	}
	return tx.Commit()
}
