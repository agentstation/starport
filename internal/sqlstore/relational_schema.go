package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type relationalKind uint8

const (
	relationalText relationalKind = iota
	relationalInteger
	relationalTime
)
const (
	migrationTable = "schema_migrations"
	metadataTable  = "sqlstore_meta"
	nameColumn     = "name"
	teamIDColumn   = "team_id"
	recordColumn   = "record"
	revisionColumn = "revision"
)

type relationalColumn struct {
	name string
	kind relationalKind
}
type relationalTable struct {
	name    string
	columns []relationalColumn
}

// relationalTables is the portable record contract for the current migration set.
// Unknown tables or columns fail transfer until their owner extends this contract.
var relationalTables = []relationalTable{
	{migrationTable, []relationalColumn{{nameColumn, relationalText}, {"applied_at", relationalTime}}},
	{metadataTable, []relationalColumn{{nameColumn, relationalText}, {"value", relationalText}}},
	{"account_templates", []relationalColumn{{"id", relationalText}, {revisionColumn, relationalInteger}, {recordColumn, relationalText}}},
	{"users", []relationalColumn{{"id", relationalText}, {"subject", relationalText}, {revisionColumn, relationalInteger}, {recordColumn, relationalText}}},
	{"teams", []relationalColumn{{"id", relationalText}, {revisionColumn, relationalInteger}, {recordColumn, relationalText}}},
	{"team_memberships", []relationalColumn{{"user_id", relationalText}, {teamIDColumn, relationalText}, {"created_at", relationalText}}},
	{"account_grants", []relationalColumn{{"account_id", relationalText}, {"user_id", relationalText}, {teamIDColumn, relationalText}, {"created_at", relationalText}}},
	{"incident_transitions", []relationalColumn{{"provider_id", relationalText}, {"indicator", relationalText}, {"description", relationalText}, {"observed_at", relationalText}}},
	{"audit_log", []relationalColumn{{"id", relationalInteger}, {"occurred_at", relationalText}, {"actor", relationalText}, {"action", relationalText}, {"subject", relationalText}, {"outcome", relationalText}, {"request_id", relationalText}}},
	{"authorization_revision", []relationalColumn{{"id", relationalInteger}, {"epoch", relationalText}, {"sequence", relationalInteger}}},
	{"catalog_recovery", []relationalColumn{{"deployment_id", relationalText}, {"epoch", relationalInteger}, {"gate_open", relationalInteger}, {"backend_id", relationalText}, {"evidence", relationalText}, {"bootstrap_allowed", relationalInteger}}},
	{"team_budget_origins", []relationalColumn{{teamIDColumn, relationalText}, {"history_id", relationalText}, {"initialize_allowed", relationalInteger}}},
	{"schema_migration_reconciliations", []relationalColumn{{"operation_id", relationalText}, {nameColumn, relationalText}, {"digest", relationalText}, {"outcome", relationalText}, {"actor", relationalText}, {"evidence_sha256", relationalText}, {"recorded_at", relationalTime}}},
}

type relationalQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateRelationalSchema(ctx context.Context, q relationalQuery, dialect string) error {
	actual, err := relationalObjects(ctx, q, dialect)
	if err != nil {
		return err
	}
	if dialect == TypeMySQL {
		if !actual["schema_migration_attempts"] {
			return errors.New("missing migration attempt table")
		}
		delete(actual, "schema_migration_attempts")
		var count int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM schema_migration_attempts").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrMigrationRecoveryRequired
		}
	}
	for _, table := range relationalTables {
		if !actual[table.name] {
			return fmt.Errorf("missing relational table %s", table.name)
		}
		delete(actual, table.name)
		if err := validateRelationalColumns(ctx, q, table); err != nil {
			return err
		}
	}
	if len(actual) != 0 {
		return errors.New("unknown relational tables prevent complete transfer")
	}
	if dialect != TypeSQLite {
		query := "SELECT count(*) FROM information_schema.triggers WHERE trigger_schema=current_schema()"
		if dialect == TypeMySQL {
			query = "SELECT count(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE()"
		}
		var count int
		if err := q.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("relational triggers prevent portable transfer")
		}
	}
	return validateTransferMigrations(ctx, q, dialect)
}

func validateRelationalColumns(ctx context.Context, q relationalQuery, table relationalTable) error {
	rows, err := q.QueryContext(ctx, "SELECT * FROM "+table.name+" WHERE 1=0")
	if err != nil {
		return err
	}
	columns, err := rows.Columns()
	if err := errors.Join(err, rows.Close()); err != nil {
		return err
	}
	wanted := make([]string, len(table.columns))
	for i, c := range table.columns {
		wanted[i] = c.name
	}
	if !slices.Equal(columns, wanted) {
		return fmt.Errorf("unsupported columns in relational table %s", table.name)
	}
	return nil
}

func validateTransferMigrations(ctx context.Context, q relationalQuery, dialect string) (resultErr error) {
	names, err := migrationNames(migrations, dialect)
	if err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, "SELECT name FROM schema_migrations ORDER BY name")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	var actual []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !slices.Equal(actual, names) {
		return errors.New("relational transfer requires the current complete migration set")
	}
	return nil
}

func (t relationalTable) columnNames() string {
	names := make([]string, len(t.columns))
	for i, c := range t.columns {
		names[i] = c.name
	}
	return strings.Join(names, ",")
}

func relationalObjects(ctx context.Context, q relationalQuery, dialect string) (map[string]bool, error) {
	query := ""
	switch dialect {
	case TypeSQLite:
		query = "SELECT name,type FROM sqlite_schema WHERE type IN ('table','view','trigger') AND name NOT GLOB 'sqlite_*' ORDER BY name"
	case TypePostgres:
		query = `SELECT c.relname, CASE WHEN c.relkind='r' AND NOT c.relrowsecurity THEN 'table' ELSE 'unsupported' END
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relkind IN ('r','p','f','v','m') ORDER BY c.relname`
	case TypeMySQL:
		query = "SELECT TABLE_NAME, CASE WHEN TABLE_TYPE='BASE TABLE' AND ENGINE='InnoDB' THEN 'table' ELSE 'unsupported' END FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() ORDER BY TABLE_NAME"
	default:
		return nil, ErrUnknownType
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	actual := map[string]bool{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if kind != "table" {
			return nil, errors.Join(fmt.Errorf("unsupported relational object %s", name), rows.Close())
		}
		actual[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return actual, nil
}
