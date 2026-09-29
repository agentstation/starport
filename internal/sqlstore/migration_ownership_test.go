package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
)

type migrationSlowSource struct{ fstest.MapFS }

func (s migrationSlowSource) ReadFile(name string) ([]byte, error) {
	// A slow source gives concurrent callers time to inspect the same history.
	time.Sleep(100 * time.Millisecond)
	return s.MapFS.ReadFile(name)
}

func TestConcurrentSQLiteMigrationStartupHasOneOwner(t *testing.T) {
	config := Config{Type: TypeSQLite, SQLite: SQLiteConfig{Path: filepath.Join(t.TempDir(), "shared.db")}}
	testConcurrentMigration(t, config)
}

func TestConcurrentNetworkMigrationStartupHasOneOwner(t *testing.T) {
	for name, config := range contractConfigs(t) {
		if name != TypeSQLite {
			t.Run(name, func(t *testing.T) { testConcurrentMigration(t, config) })
		}
	}
}

func testConcurrentMigration(t *testing.T, config Config) {
	t.Helper()
	const callers = 16
	connections := make([]*DB, 0, callers)
	for range callers {
		db, err := Open(config)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })
		require.NoError(t, db.Ping(t.Context()))
		connections = append(connections, db)
	}
	_, err := connections[0].ExecContext(t.Context(), connections[0].schemaMigrationsDDL())
	require.NoError(t, err)
	source := migrationSlowSource{fstest.MapFS{"migrations/" + config.Type + "/0001_probe.sql": {Data: []byte(`CREATE TABLE migration_probe (id INTEGER PRIMARY KEY); INSERT INTO migration_probe VALUES (1);`)}}}
	start := make(chan struct{})
	results := make(chan error, callers)
	for _, db := range connections {
		go func() {
			<-start
			results <- db.migrate(t.Context(), source)
		}()
	}
	close(start)
	var failures []error
	for range callers {
		if err := <-results; err != nil {
			failures = append(failures, err)
		}
	}
	require.Empty(t, failures, "all concurrent startup callers must observe one successful migration")
	var count int
	require.NoError(t, connections[0].QueryRowContext(t.Context(), "SELECT COUNT(*) FROM migration_probe").Scan(&count))
	require.Equal(t, 1, count)
}

func TestMigrationOwnershipAcrossProcesses(t *testing.T) {
	if raw := os.Getenv("STARPORT_TEST_MIGRATION_CONFIG"); raw != "" {
		var config Config
		require.NoError(t, json.Unmarshal([]byte(raw), &config))
		db, err := Open(config)
		require.NoError(t, err)
		defer db.Close()
		source := migrationSlowSource{fstest.MapFS{"migrations/" + config.Type + "/0001_probe.sql": {Data: []byte(`CREATE TABLE migration_probe (id INTEGER PRIMARY KEY); INSERT INTO migration_probe VALUES (1);`)}}}
		require.NoError(t, db.migrate(t.Context(), source))
		return
	}
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(config)
			require.NoError(t, err)
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMigrationOwnershipAcrossProcesses$", "-test.timeout=25s")
					cmd.Env = append(os.Environ(), "STARPORT_TEST_MIGRATION_CONFIG="+string(body))
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Errorf("migration process: %v\n%s", err, output)
					}
				})
			}
			wg.Wait()
			db, err := Open(config)
			require.NoError(t, err)
			defer db.Close()
			var count int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM migration_probe").Scan(&count))
			require.Equal(t, 1, count)
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}

func TestMigrationOwnerCancellationAndRelease(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			first, err := Open(config)
			require.NoError(t, err)
			defer first.Close()
			second, err := Open(config)
			require.NoError(t, err)
			defer second.Close()
			require.NoError(t, first.Ping(t.Context()))
			require.NoError(t, second.Ping(t.Context()))
			owner, err := first.acquireMigrationOwner(t.Context())
			require.NoError(t, err)
			defer owner.discard()
			if name == TypeSQLite {
				_, err = owner.conn.ExecContext(t.Context(), "BEGIN IMMEDIATE")
				require.NoError(t, err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			err = second.Migrate(ctx)
			require.Error(t, err)
			require.Less(t, time.Since(start), 2*time.Second, "cancellation must bound ownership wait")
			if name == TypeSQLite {
				_, err = owner.conn.ExecContext(t.Context(), "ROLLBACK")
				require.NoError(t, err)
			}
			require.NoError(t, owner.close())
			require.NoError(t, second.Migrate(t.Context()), fmt.Sprintf("ownership must be released for %s", name))
		})
	}
}

func TestMigrationRejectsUnknownHistory(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			db, err := Open(config)
			require.NoError(t, err)
			defer db.Close()
			require.NoError(t, db.Migrate(t.Context()))
			_, err = db.ExecContext(t.Context(), "INSERT INTO schema_migrations (name) VALUES ('9999_future.sql')")
			require.NoError(t, err)
			require.ErrorContains(t, db.Migrate(t.Context()), "unsupported schema migration")
		})
	}
}

func TestMySQLPartialMigrationRequiresReconciliation(t *testing.T) {
	address := os.Getenv("TEST_MYSQL_DSN")
	if address == "" {
		t.Skip("TEST_MYSQL_DSN is required")
	}
	config := isolatedContractConfig(t, Config{Type: TypeMySQL, MySQL: MySQLConfig{DSN: address}})
	db, err := Open(config)
	require.NoError(t, err)
	defer db.Close()
	source := fstest.MapFS{"migrations/mysql/0001_partial.sql": {Data: []byte("CREATE TABLE partial_probe (id INTEGER); INVALID STATEMENT;")}}
	require.ErrorIs(t, db.migrate(t.Context(), source), ErrMigrationRecoveryRequired)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM partial_probe").Scan(&count))
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migration_attempts").Scan(&count))
	require.Equal(t, 1, count)
	// Corrected source cannot silently repeat previously committed statements.
	source["migrations/mysql/0001_partial.sql"].Data = []byte("INSERT INTO partial_probe VALUES (1);")
	require.ErrorIs(t, db.migrate(t.Context(), source), ErrMigrationRecoveryRequired)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM partial_probe").Scan(&count))
	require.Zero(t, count)
}
