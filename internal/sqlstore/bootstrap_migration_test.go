package sqlstore

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestCatalogBootstrapMigrationDefaultsToDenied(t *testing.T) {
	configs := map[string]Config{"sqlite": {Type: TypeSQLite}}
	if address := os.Getenv("TEST_POSTGRES_URL"); address != "" {
		configs["postgres"] = isolatedContractConfig(t, Config{Type: TypePostgres, Postgres: PostgresConfig{URL: address}})
	}
	for name, config := range configs {
		t.Run(name, func(t *testing.T) {
			db, err := Open(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			previous := fstest.MapFS{}
			err = fs.WalkDir(migrations, "migrations", func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() || strings.HasSuffix(path, "0010_catalog_bootstrap.sql") {
					return err
				}
				data, err := fs.ReadFile(migrations, path)
				previous[path] = &fstest.MapFile{Data: data}
				return err
			})
			require.NoError(t, err)
			require.NoError(t, db.migrate(t.Context(), previous))
			_, err = db.ExecContext(t.Context(), "INSERT INTO catalog_recovery (deployment_id, epoch, gate_open, backend_id, evidence) VALUES ('existing', 1, 1, 'backend', 'prior-approval')")
			require.NoError(t, err)
			require.NoError(t, db.Migrate(t.Context()))
			require.NoError(t, db.Migrate(t.Context()))
			var allowed int
			err = db.QueryRowContext(t.Context(), "SELECT bootstrap_allowed FROM catalog_recovery WHERE deployment_id = 'existing'").Scan(&allowed)
			require.NoError(t, err)
			require.Zero(t, allowed, "migration must not infer fresh permission from pre-existing approval")
		})
	}
}
