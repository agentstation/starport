package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrAuditCounterUnrepresentable refuses a target that could reuse an exhausted audit ID.
var ErrAuditCounterUnrepresentable = errors.New("MySQL cannot preserve an exhausted audit allocation counter")

type relationalWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func copyRelationalRows(ctx context.Context, source relationalQuery, target relationalWriter, dialect string) (resultErr error) {
	for _, table := range relationalTables {
		if err := copyRelationalTable(ctx, source, target, dialect, table); err != nil {
			return fmt.Errorf("copy %s: %w", table.name, err)
		}
	}
	return nil
}

func copyRelationalTable(ctx context.Context, source relationalQuery, target relationalWriter, dialect string, table relationalTable) (resultErr error) {
	rows, err := source.QueryContext(ctx, "SELECT "+table.columnNames()+" FROM "+table.name)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	markers := strings.TrimSuffix(strings.Repeat("?,", len(table.columns)), ",")
	override := ""
	if dialect == TypePostgres && table.name == "audit_log" {
		override = " OVERRIDING SYSTEM VALUE"
	}
	query := (&DB{dialect: dialect}).Bind("INSERT INTO " + table.name + " (" + table.columnNames() + ")" + override + " VALUES (" + markers + ")")
	for rows.Next() {
		values, err := scanRelationalRow(rows, table, dialect)
		if err != nil {
			return err
		}
		if _, err := target.ExecContext(ctx, query, values...); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scanRelationalRow(rows *sql.Rows, table relationalTable, targetDialect string) ([]any, error) {
	pointers := make([]any, len(table.columns))
	for i, c := range table.columns {
		switch c.kind {
		case relationalInteger:
			pointers[i] = new(int64)
		case relationalText:
			pointers[i] = new(string)
		case relationalTime:
			pointers[i] = new(time.Time)
		default:
			return nil, errors.New("unknown relational column kind")
		}
	}
	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}
	values := make([]any, len(pointers))
	for i, p := range pointers {
		switch v := p.(type) {
		case *int64:
			values[i] = *v
		case *string:
			values[i] = *v
		case *time.Time:
			if (targetDialect == TypeMySQL || targetDialect == TypePostgres) && v.Nanosecond()%1000 != 0 {
				return nil, errors.New("network SQL timestamps cannot preserve sub-microsecond migration evidence")
			}
			values[i] = v.UTC()
		}
	}
	return values, nil
}

func auditHighWater(ctx context.Context, q relationalQuery, dialect string) (int64, error) {
	var high int64
	switch dialect {
	case TypeSQLite:
		err := q.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name='audit_log'").Scan(&high)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	case TypePostgres:
		var called bool
		if err := q.QueryRowContext(ctx, "SELECT last_value,is_called FROM audit_log_id_seq").Scan(&high, &called); err != nil {
			return 0, err
		}
		if !called {
			high--
		}
	case TypeMySQL:
		var raw string
		if err := q.QueryRowContext(ctx, "SELECT COALESCE(AUTO_INCREMENT,1) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='audit_log'").Scan(&raw); err != nil {
			return 0, err
		}
		next, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || next == 0 || next > uint64(math.MaxInt64)+1 {
			return 0, errors.New("invalid audit allocation counter")
		}
		high = int64(next - 1) // #nosec G115 -- the prior check bounds next to MaxInt64 + 1.
		// MySQL clamps AUTO_INCREMENT at its signed limit, even after consuming that ID.
		if next >= uint64(math.MaxInt64) {
			high = math.MaxInt64
		}
	default:
		return 0, ErrUnknownType
	}
	var maxID int64
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM audit_log").Scan(&maxID); err != nil {
		return 0, err
	}
	if high < 0 || maxID < 0 {
		return 0, errors.New("negative audit allocation counter")
	}
	return max(high, maxID), nil
}

func restoreAuditHighWater(ctx context.Context, q relationalQuery, w relationalWriter, dialect string, high int64) error {
	if high < 0 {
		return errors.New("negative audit allocation counter")
	}
	if dialect == TypeMySQL && high == math.MaxInt64 {
		return ErrAuditCounterUnrepresentable
	}
	if dialect == TypePostgres {
		next := high + 1
		if high == math.MaxInt64 {
			next = high
		}
		// RESTART is transactional. setval would survive a later rollback.
		if _, err := w.ExecContext(ctx, "ALTER SEQUENCE audit_log_id_seq RESTART WITH "+strconv.FormatInt(next, 10)); err != nil {
			return err
		}
		if high == math.MaxInt64 {
			_, err := w.ExecContext(ctx, "SELECT nextval('audit_log_id_seq')")
			return err
		}
		return nil
	}
	var maxID int64
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM audit_log").Scan(&maxID); err != nil {
		return err
	}
	if high <= maxID {
		return nil
	}
	// A temporary explicit ID advances the native allocator without retaining an audit event.
	if _, err := w.ExecContext(ctx, "INSERT INTO audit_log(id,occurred_at,actor,action,subject,outcome,request_id) VALUES(?,'','','','','','')", high); err != nil {
		return err
	}
	_, err := w.ExecContext(ctx, "DELETE FROM audit_log WHERE id=?", high)
	return err
}

// prepareMySQLTransfer fixes session conversion and metadata rules for this operation.
func prepareMySQLTransfer(ctx context.Context, owner *migrationOwner) (func() error, error) {
	if owner.dialect != TypeMySQL {
		return func() error { return nil }, nil
	}
	var zone, mode string
	var expiry int64
	if err := owner.conn.QueryRowContext(ctx, "SELECT @@session.time_zone,@@session.sql_mode,@@session.information_schema_stats_expiry").Scan(&zone, &mode, &expiry); err != nil {
		return nil, err
	}
	cleanup := func() error {
		bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := owner.conn.ExecContext(bounded, "SET SESSION time_zone=?, SESSION sql_mode=?, SESSION information_schema_stats_expiry=?", zone, mode, expiry)
		if err != nil {
			owner.discard()
		}
		return err
	}
	if _, err := owner.conn.ExecContext(ctx, "SET SESSION time_zone='+00:00', SESSION sql_mode='STRICT_ALL_TABLES,NO_ZERO_DATE,NO_ZERO_IN_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION', SESSION information_schema_stats_expiry=0"); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	return cleanup, nil
}
