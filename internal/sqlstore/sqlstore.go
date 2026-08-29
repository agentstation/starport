// Package sqlstore owns Starport's relational storage contract. It is the
// SQL twin of internal/storage: where that package pairs an embedded Badger
// store with a Valkey connect for shared state, this one pairs an embedded
// SQLite database that rides the binary with a network SQL connect for a
// multi-node deployment. Relational concepts — identity, teams, grants,
// account templates — keep their repositories on this contract and never
// name a driver or a dialect themselves.
//
// The embedded backend uses a pure-Go SQLite driver, so the single-binary
// install story holds: no cgo, no system library, no separate database
// process. The package also owns the schema: every migration lives in the
// embedded migrations directory here, and Migrate applies them in order,
// once each, recording what it applied. No other package writes schema.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Relational backend type constants.
const (
	// TypeSQLite is the embedded backend: a database file beside the
	// deployment's other data, created on first open. An empty path keeps
	// the database in memory, which is the development runtime's choice.
	TypeSQLite = "sqlite"
	// TypePostgres is the PostgreSQL connect for a multi-node deployment,
	// the relational counterpart of the Valkey connect.
	TypePostgres = "postgres"
	// TypeMySQL is the MySQL connect for a multi-node deployment.
	TypeMySQL = "mysql"
)

// Common errors returned by sqlstore operations.
var (
	// ErrUnknownType is returned when the configuration names a backend
	// this package does not serve.
	ErrUnknownType = errors.New("unknown sqlstore type")
	// ErrClosed is returned when an operation reaches a closed store.
	ErrClosed = errors.New("sqlstore closed")
)

// Config selects and configures a relational backend, the way
// storage.Config selects a key-value one.
type Config struct {
	Type     string         `env:"TYPE,default=sqlite"`
	SQLite   SQLiteConfig   `env:",prefix=SQLITE_"`
	Postgres PostgresConfig `env:",prefix=POSTGRES_"`
	MySQL    MySQLConfig    `env:",prefix=MYSQL_"`
}

// SQLiteConfig configures the embedded backend.
type SQLiteConfig struct {
	// Path is the database file. An empty path opens an in-memory
	// database that lives and dies with the process.
	Path string `env:"PATH"`
}

// PostgresConfig configures the PostgreSQL connect.
type PostgresConfig struct {
	// URL is a postgres:// connection URL, database and credentials
	// included.
	URL string `env:"URL"`
}

// MySQLConfig configures the MySQL connect.
type MySQLConfig struct {
	// DSN is a go-sql-driver DSN such as
	// user:password@tcp(host:3306)/starport.
	DSN string `env:"DSN"`
}

// Validate reports a configuration this package cannot open.
func (c Config) Validate() error {
	switch c.Type {
	case TypeSQLite:
		return nil
	case TypePostgres:
		if c.Postgres.URL == "" {
			return fmt.Errorf("postgres sqlstore requires a URL")
		}
		return nil
	case TypeMySQL:
		if c.MySQL.DSN == "" {
			return fmt.Errorf("mysql sqlstore requires a DSN")
		}
		return nil
	case "":
		return fmt.Errorf("%w: empty", ErrUnknownType)
	default:
		return fmt.Errorf("%w: %s", ErrUnknownType, c.Type)
	}
}

// DB is one open relational store. It embeds the standard connection pool,
// so a repository reads and writes through database/sql, and it names the
// dialect for the rare statement that cannot be written once for every
// backend.
type DB struct {
	*sql.DB
	dialect string
}

// Dialect names the SQL dialect this store speaks: one of the Type
// constants.
func (db *DB) Dialect() string { return db.dialect }

// Ping reports whether the store is reachable, bounded by the context.
func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}
