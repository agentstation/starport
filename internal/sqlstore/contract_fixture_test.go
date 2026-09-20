package sqlstore

import (
	"context"
	"crypto/rand"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

// isolatedContractConfig keeps each test in a namespace that it owns.
func isolatedContractConfig(t *testing.T, config Config) Config {
	t.Helper()
	admin, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	name := "starport_contract_" + strings.ToLower(rand.Text())
	var create, drop string
	switch config.Type {
	case TypePostgres:
		parsed, err := url.Parse(config.Postgres.URL)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", name)
		parsed.RawQuery = query.Encode()
		config.Postgres.URL = parsed.String()
		quoted := pgx.Identifier{name}.Sanitize()
		create, drop = "CREATE SCHEMA "+quoted, "DROP SCHEMA "+quoted+" CASCADE"
	case TypeMySQL:
		parsed, err := mysql.ParseDSN(config.MySQL.DSN)
		if err != nil {
			t.Fatal(err)
		}
		parsed.DBName = name
		config.MySQL.DSN = parsed.FormatDSN()
		quoted := "`" + name + "`"
		create, drop = "CREATE DATABASE "+quoted, "DROP DATABASE "+quoted
	default:
		t.Fatalf("unsupported network fixture: %s", config.Type)
	}
	if _, err := admin.ExecContext(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, drop); err != nil {
			t.Error(err)
		}
	})
	return config
}

func TestContractFixturesIsolateSchema(t *testing.T) {
	first, second := contractConfigs(t), contractConfigs(t)
	for name, config := range first {
		t.Run(name, func(t *testing.T) {
			for i, selected := range []Config{config, second[name]} {
				db, err := Open(selected)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if err := db.Migrate(t.Context()); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlstore_meta WHERE name = 'isolation'").Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("fixture %d inherited %d rows", i, count)
				}
				if _, err := db.ExecContext(t.Context(), "INSERT INTO sqlstore_meta (name, value) VALUES ('isolation', 'owned')"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
