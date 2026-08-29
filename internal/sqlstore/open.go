package sqlstore

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	// The embedded backend. Pure Go: no cgo, no system SQLite, so the
	// single static binary the release ships stays single and static.
	_ "modernc.org/sqlite"
)

// Open creates a relational store from the configuration. It follows the Go
// convention internal/storage.Open set: validate, then dispatch on the type.
func Open(config Config) (*DB, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid sqlstore config: %w", err)
	}

	switch config.Type {
	case TypeSQLite:
		return openSQLite(config.SQLite)
	case TypePostgres:
		return openPostgres(config.Postgres)
	case TypeMySQL:
		return openMySQL(config.MySQL)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownType, config.Type)
	}
}

// openSQLite opens the embedded database. A path opens (and on first use
// creates) a file; an empty path keeps the database in memory for the
// process's lifetime, which is what the development runtime wants.
func openSQLite(config SQLiteConfig) (*DB, error) {
	dsn := "file::memory:?" + sqliteOptions
	if config.Path != "" {
		if err := os.MkdirAll(filepath.Dir(config.Path), 0o750); err != nil {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
		dsn = "file:" + url.PathEscape(config.Path) + "?" + sqliteOptions
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite serves one writer at a time. One connection makes the pool
	// agree with the engine instead of discovering it as SQLITE_BUSY, and
	// it is also what keeps an in-memory database from vanishing between
	// pooled connections.
	db.SetMaxOpenConns(1)
	return &DB{DB: db, dialect: TypeSQLite}, nil
}

// sqliteOptions are the pragmas every open uses. Write-ahead logging keeps a
// reader from blocking the writer, foreign keys make the schema's stated
// relations real, and the busy timeout turns a locked database into a bounded
// wait instead of an immediate error.
const sqliteOptions = "_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
