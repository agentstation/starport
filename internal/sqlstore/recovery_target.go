package sqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

// RecoveryTargetSHA256 binds connection routes and the selected database namespace.
// It excludes credentials and does not prove physical server continuity.
func (db *DB) RecoveryTargetSHA256(ctx context.Context, config Config) (string, error) {
	if ctx == nil || db == nil || db.DB == nil || config.Type != db.dialect {
		return "", ErrClosed
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var scope []string
	switch db.dialect {
	case TypeSQLite:
		identity, err := recoverySQLiteIdentity(config.SQLite.Path)
		if err != nil {
			return "", err
		}
		scope = []string{TypeSQLite, config.SQLite.Path, identity}
	case TypePostgres:
		parsed, err := pgx.ParseConfig(config.Postgres.URL)
		if err != nil {
			return "", errors.New("recovery target PostgreSQL configuration is invalid")
		}
		var database, schemas string
		if err := db.QueryRowContext(ctx, "SELECT current_database(), current_schemas(true)::text").Scan(&database, &schemas); err != nil {
			return "", errors.New("recovery target PostgreSQL namespace is unavailable")
		}
		scope = []string{TypePostgres, parsed.Host, strconv.Itoa(int(parsed.Port)), database, schemas}
		for _, fallback := range parsed.Fallbacks {
			scope = append(scope, fallback.Host, strconv.Itoa(int(fallback.Port)))
		}
	case TypeMySQL:
		parsed, err := mysql.ParseDSN(config.MySQL.DSN)
		if err != nil {
			return "", errors.New("recovery target MySQL configuration is invalid")
		}
		var database string
		if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
			return "", errors.New("recovery target MySQL namespace is unavailable")
		}
		scope = []string{TypeMySQL, parsed.Net, parsed.Addr, database}
	default:
		return "", ErrUnknownType
	}
	body, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func recoverySQLiteIdentity(path string) (identity string, resultErr error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("recovery target requires an absolute persistent SQLite path")
	}
	directory, err := productfiles.ExistingDirectory(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	root, err := directory.Open()
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil {
		return "", err
	}
	current, err := root.Lstat(filepath.Base(path))
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return "", errors.New("recovery target SQLite file identity changed")
	}
	return productfiles.FileIdentity(file)
}
