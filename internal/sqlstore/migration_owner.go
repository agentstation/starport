package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
)

// migrationOwner retains the connection that owns the database migration lock.
// SQLite takes its writer lock separately for each migration transaction.
type migrationOwner struct {
	conn    *sql.Conn
	dialect string
	lock    string
}

func (db *DB) acquireMigrationOwner(ctx context.Context) (*migrationOwner, error) {
	var conn *sql.Conn
	for {
		var err error
		conn, err = db.Conn(ctx)
		if err == nil {
			break
		}
		if db.dialect != TypeSQLite || !sqliteMigrationBusy(err) {
			return nil, err
		}
		if err := waitMigrationRetry(ctx); err != nil {
			return nil, err
		}
	}
	owner := &migrationOwner{conn: conn, dialect: db.dialect}
	if err := owner.acquire(ctx); err != nil {
		// A canceled reply cannot prove that the server did not grant the lock.
		owner.discard()
		return nil, fmt.Errorf("acquire migration ownership: %w", err)
	}
	return owner, nil
}

func (o *migrationOwner) acquire(ctx context.Context) error {
	var scope string
	switch o.dialect {
	case TypeSQLite:
		_, err := o.conn.ExecContext(ctx, "PRAGMA busy_timeout = 50")
		return err
	case TypePostgres:
		if err := o.conn.QueryRowContext(ctx, "SELECT current_schema()").Scan(&scope); err != nil {
			return err
		}
	case TypeMySQL:
		if err := o.conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&scope); err != nil {
			return err
		}
	default:
		return ErrUnknownType
	}
	if scope == "" {
		return errors.New("migration schema is empty")
	}
	// MySQL lock names permit at most 64 characters.
	o.lock = fmt.Sprintf("%x", sha256.Sum256([]byte("starport:migrations:"+scope)))
	if o.dialect == TypePostgres {
		_, err := o.conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", o.lock)
		return err
	}
	for {
		var acquired sql.NullInt64
		if err := o.conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", o.lock).Scan(&acquired); err != nil {
			return err
		}
		if !acquired.Valid {
			return errors.New("database could not acquire migration lock")
		}
		if acquired.Int64 == 1 {
			return nil
		}
		if err := waitMigrationRetry(ctx); err != nil {
			return err
		}
	}
}

func (o *migrationOwner) discard() {
	_ = o.conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = o.conn.Close()
}

func (o *migrationOwner) close() error {
	if o.dialect == TypeSQLite {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := o.conn.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
			o.discard()
			return err
		}
	}
	if o.lock != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		query := "SELECT RELEASE_LOCK(?)"
		if o.dialect == TypePostgres {
			query = "SELECT pg_advisory_unlock(hashtextextended($1, 0))"
		}
		err := o.conn.QueryRowContext(ctx, query, o.lock).Scan(&released)
		if err != nil || !released {
			o.discard()
			return fmt.Errorf("release migration ownership: %w", errors.Join(err, errors.New("ownership release is unconfirmed")))
		}
	}
	return o.conn.Close()
}

// transaction keeps history checks, schema changes, and their receipt together.
// MySQL DDL can commit implicitly and still requires partial-migration recovery.
func (o *migrationOwner) transaction(ctx context.Context, apply func() error) (err error) {
	begin := "BEGIN"
	if o.dialect == TypeSQLite {
		begin = "BEGIN IMMEDIATE"
	}
	for {
		if _, err = o.conn.ExecContext(ctx, begin); err == nil {
			break
		}
		if o.dialect != TypeSQLite || !sqliteMigrationBusy(err) {
			return err
		}
		if err = waitMigrationRetry(ctx); err != nil {
			return err
		}
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, rollbackErr := o.conn.ExecContext(cleanup, "ROLLBACK"); rollbackErr != nil {
			o.discard()
			err = errors.Join(err, fmt.Errorf("rollback migration: %w", rollbackErr))
		}
	}()
	if err = apply(); err != nil {
		return err
	}
	_, err = o.conn.ExecContext(ctx, "COMMIT")
	committed = err == nil
	return err
}

func sqliteMigrationBusy(err error) bool {
	code, ok := errors.AsType[*sqlite.Error](err)
	return ok && (code.Code()&0xff == 5 || code.Code()&0xff == 6)
}

func waitMigrationRetry(ctx context.Context) error {
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
