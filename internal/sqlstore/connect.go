package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// The MySQL connect. Pure Go, like every driver this package ships.
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// The network backends are the scaling half of the pairing: where a
// single-node deployment rides the embedded SQLite file, a multi-node one
// points every gateway at a shared PostgreSQL or MySQL server, exactly as
// Valkey stands beside the embedded Badger store. Both opens verify the
// server is reachable before returning, because a network address that
// cannot answer is a configuration error the operator should see at
// startup, not at first use.

// openPostgres opens the PostgreSQL connect.
func openPostgres(config PostgresConfig) (*DB, error) {
	connConfig, err := pgx.ParseConfig(config.URL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	// Migrations are multi-statement files, and the extended protocol
	// refuses more than one command per call. The simple protocol runs
	// them whole; pgx still binds arguments safely under it.
	connConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := sql.OpenDB(stdlib.GetConnector(*connConfig))
	return openConnect(db, TypePostgres, "postgres")
}

// openMySQL opens the MySQL connect.
func openMySQL(config MySQLConfig) (*DB, error) {
	dsnConfig, err := mysql.ParseDSN(config.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse mysql dsn: %w", err)
	}
	// Multi-statement migration files, and times that scan as time.Time.
	dsnConfig.MultiStatements = true
	dsnConfig.ParseTime = true
	connector, err := mysql.NewConnector(dsnConfig)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	return openConnect(sql.OpenDB(connector), TypeMySQL, "mysql")
}

// openConnect applies the pool settings a shared server wants and proves
// the server answers.
func openConnect(db *sql.DB, dialect, name string) (*DB, error) {
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)

	store := &DB{DB: db, dialect: dialect}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to %s: %w", name, err)
	}
	return store, nil
}
