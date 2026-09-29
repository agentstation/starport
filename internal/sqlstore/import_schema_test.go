package sqlstore

import (
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestPrepareImportSchemaNativeBackends(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"empty", "partial-empty", "partial-populated", "null-metadata", "unknown-table", "current"} {
				t.Run(mode, func(t *testing.T) {
					selected := config
					if config.Type == TypeSQLite {
						selected.SQLite.Path = filepath.Join(t.TempDir(), "target.db")
					} else {
						selected = isolatedContractConfig(t, config)
					}
					db, err := Open(selected)
					require.NoError(t, err)
					defer func() { require.NoError(t, db.Close()) }()
					switch mode {
					case "partial-empty":
						// The migration runner resumes an interrupted setup at its completed file boundary.
						names, err := migrationNames(migrations, db.dialect)
						require.NoError(t, err)
						fsys := firstMigrationFixture(t, db.dialect, names[0])
						require.NoError(t, db.migrate(t.Context(), fsys))
					case "partial-populated":
						_, err := db.ExecContext(t.Context(), "CREATE TABLE users (id VARCHAR(191) PRIMARY KEY)")
						require.NoError(t, err)
						_, err = db.ExecContext(t.Context(), "INSERT INTO users(id) VALUES('keep')")
						require.NoError(t, err)
					case "null-metadata":
						_, err := db.ExecContext(t.Context(), "CREATE TABLE sqlstore_meta (name VARCHAR(191), value VARCHAR(191))")
						require.NoError(t, err)
						_, err = db.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES(NULL,NULL)")
						require.NoError(t, err)
					case "unknown-table":
						_, err := db.ExecContext(t.Context(), "CREATE TABLE operator_data (id INTEGER)")
						require.NoError(t, err)
					case "current":
						require.NoError(t, db.Migrate(t.Context()))
					}
					err = db.PrepareImportSchema(t.Context())
					if mode == "partial-populated" || mode == "unknown-table" || mode == "null-metadata" {
						require.Error(t, err)
						if mode == "null-metadata" {
							objects, err := relationalObjects(t.Context(), db, db.dialect)
							require.NoError(t, err)
							require.Equal(t, map[string]bool{"sqlstore_meta": true}, objects)
						}
						if mode == "partial-populated" {
							var value string
							require.NoError(t, db.QueryRowContext(t.Context(), "SELECT id FROM users").Scan(&value))
							require.Equal(t, "keep", value)
						}
						return
					}
					require.NoError(t, err)
					require.NoError(t, validateRelationalSchema(t.Context(), db, db.dialect))
					require.NoError(t, db.PrepareImportSchema(t.Context()))
				})
			}
		})
	}
}

func firstMigrationFixture(t *testing.T, dialect, name string) fstest.MapFS {
	t.Helper()
	path := "migrations/" + dialect + "/" + name
	body, err := migrations.ReadFile(path)
	require.NoError(t, err)
	return fstest.MapFS{path: {Data: body}}
}
