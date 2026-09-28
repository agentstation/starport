package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/stretchr/testify/require"
)

func transferDB(t *testing.T, config Config) *DB {
	t.Helper()
	db, err := Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	return db
}

func transferDirectory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private")
	_, err := productfiles.CreateDirectory(path)
	require.NoError(t, err)
	return path
}

func seedRelationalTransfer(t *testing.T, db *DB) {
	t.Helper()
	statements := []string{
		"INSERT INTO sqlstore_meta VALUES('transfer-proof','世界')",
		"INSERT INTO account_templates VALUES('template',9223372036854775807,'{\"defaults\":true}')",
		"INSERT INTO users VALUES('user','provider:subject',9,'{\"name\":\"example\"}')",
		"INSERT INTO teams VALUES('team',5,'{}')",
		"INSERT INTO team_memberships VALUES('user','team','2026-09-28T00:00:00.123456789Z')",
		"INSERT INTO account_grants VALUES('account','user','','2026-09-28T00:00:00Z')",
		"INSERT INTO incident_transitions VALUES('provider','degraded','test incident','2026-09-28T00:00:00Z')",
		"INSERT INTO authorization_revision VALUES(1,'epoch',9223372036854775807)",
		"INSERT INTO catalog_recovery VALUES('deployment',19,1,'old-primary','old-proof',1)",
		"INSERT INTO team_budget_origins VALUES('team','history',0)",
	}
	for _, query := range statements {
		_, err := db.ExecContext(t.Context(), query)
		require.NoError(t, err)
	}
	_, err := db.ExecContext(t.Context(), db.Bind("INSERT INTO schema_migration_reconciliations(operation_id,name,digest,outcome,actor,evidence_sha256) VALUES(?,?,?,?,?,?)"), "repair", "0007_audit_request_id.sql", strings.Repeat("1", 64), "applied", "operator", strings.Repeat("2", 64))
	require.NoError(t, err)
	override := ""
	if db.dialect == TypePostgres {
		override = " OVERRIDING SYSTEM VALUE"
	}
	_, err = db.ExecContext(t.Context(), "INSERT INTO audit_log(id,occurred_at,actor,action,subject,outcome,request_id)"+override+" VALUES(41,'2026-09-28T00:00:00Z','operator','inspect','deployment','ok','request')")
	require.NoError(t, err)
	require.NoError(t, restoreAuditHighWater(t.Context(), db, db, db.dialect, 100))
}

func restrictImportedFixture(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET gate_open=0,bootstrap_allowed=0; UPDATE team_budget_origins SET initialize_allowed=0")
	return err
}

func TestRelationalTransferAllBackendPairs(t *testing.T) {
	for sourceName, sourceConfig := range contractConfigs(t) {
		t.Run(sourceName, func(t *testing.T) {
			source := transferDB(t, sourceConfig)
			seedRelationalTransfer(t, source)
			directory := transferDirectory(t)
			snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(directory, "snapshot"))
			require.NoError(t, err)
			require.True(t, snapshot.Published)
			for targetName, targetConfig := range contractConfigs(t) {
				t.Run(targetName, func(t *testing.T) {
					target := transferDB(t, targetConfig)
					require.NoError(t, target.ImportRelational(t.Context(), filepath.Join(directory, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), restrictImportedFixture))
					for _, table := range relationalTables {
						var original, restored int
						require.NoError(t, source.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table.name).Scan(&original))
						require.NoError(t, target.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table.name).Scan(&restored))
						require.Equal(t, original, restored, table.name)
						expectedRows := relationalFixtureRows(t, source, table)
						if table.name == "catalog_recovery" {
							for _, row := range expectedRows {
								row[2] = "0"
								row[5] = "0"
							}
						}
						require.Equal(t, expectedRows, relationalFixtureRows(t, target, table), table.name)
					}
					var revision int64
					var value string
					require.NoError(t, target.QueryRowContext(t.Context(), "SELECT revision FROM account_templates WHERE id='template'").Scan(&revision))
					require.Equal(t, int64(9223372036854775807), revision)
					require.NoError(t, target.QueryRowContext(t.Context(), "SELECT value FROM sqlstore_meta WHERE name='transfer-proof'").Scan(&value))
					require.Equal(t, "世界", value)
					require.NoError(t, target.QueryRowContext(t.Context(), "SELECT created_at FROM team_memberships WHERE user_id='user' AND team_id='team'").Scan(&value))
					require.Equal(t, "2026-09-28T00:00:00.123456789Z", value)
					var gate, bootstrap int
					var epoch int64
					require.NoError(t, target.QueryRowContext(t.Context(), "SELECT epoch,gate_open,bootstrap_allowed FROM catalog_recovery WHERE deployment_id='deployment'").Scan(&epoch, &gate, &bootstrap))
					require.Equal(t, int64(19), epoch)
					require.Zero(t, gate)
					require.Zero(t, bootstrap)
					_, err := target.ExecContext(t.Context(), "INSERT INTO audit_log(occurred_at,actor,action,subject,outcome,request_id) VALUES('now','operator','after','deployment','ok','new-request')")
					require.NoError(t, err)
					var newID int64
					require.NoError(t, target.QueryRowContext(t.Context(), "SELECT id FROM audit_log WHERE request_id='new-request'").Scan(&newID))
					require.Equal(t, int64(101), newID, "deleted audit IDs must not be reused")
					require.ErrorIs(t, target.ImportRelational(t.Context(), filepath.Join(directory, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), restrictImportedFixture), ErrNotFresh)
				})
			}
		})
	}
}

func TestRelationalTransferRestrictionFailureRollsBack(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			marker := errors.New("independent history unavailable")
			restrict := func(ctx context.Context, conn *sql.Conn) error {
				require.NoError(t, restrictImportedFixture(ctx, conn))
				return marker
			}
			require.ErrorIs(t, target.ImportRelational(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), restrict), marker)
			var count int
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT count(*) FROM users").Scan(&count))
			require.Zero(t, count)
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_recovery").Scan(&count))
			require.Zero(t, count)
			require.NoError(t, target.ImportRelational(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), restrictImportedFixture))
		})
	}
}

func TestRelationalTransferRefusesUnknownOrPartialState(t *testing.T) {
	for _, scenario := range []string{"table", "sqlite-prefix", "column", "view", "trigger", "history"} {
		t.Run(scenario, func(t *testing.T) {
			db, parent := sqliteSnapshotFixture(t)
			query := map[string]string{"table": "CREATE TABLE private_extension(secret TEXT)", "sqlite-prefix": "CREATE TABLE sqliteextension(secret TEXT)", "column": "ALTER TABLE users ADD COLUMN extra TEXT", "view": "CREATE VIEW extra_view AS SELECT id FROM users", "trigger": "CREATE TRIGGER extra_trigger AFTER INSERT ON users BEGIN DELETE FROM users; END", "history": "DELETE FROM schema_migrations WHERE name='0012_migration_reconciliation.sql'"}[scenario]
			_, err := db.ExecContext(t.Context(), query)
			require.NoError(t, err)
			result, err := db.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
			require.Error(t, err)
			require.False(t, result.Published)
		})
	}
}

func TestRelationalTransferConcurrentImportHasOneWinner(t *testing.T) {
	source, _ := sqliteSnapshotFixture(t)
	seedRelationalTransfer(t, source)
	parent := transferDirectory(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			var wg sync.WaitGroup
			results := make(chan error, 4)
			for range 4 {
				scratch := transferDirectory(t)
				wg.Go(func() {
					results <- target.ImportRelational(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, scratch, restrictImportedFixture)
				})
			}
			wg.Wait()
			close(results)
			winners := 0
			for err := range results {
				if err == nil {
					winners++
				} else {
					require.ErrorIs(t, err, ErrNotFresh)
				}
			}
			require.Equal(t, 1, winners)
		})
	}
}

func TestRelationalTransferCancellationAndIdentity(t *testing.T) {
	source, parent := sqliteSnapshotFixture(t)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	target, _ := sqliteSnapshotFixture(t)
	wrong := snapshot.Snapshot
	wrong.SHA256 = strings.Repeat("0", 64)
	require.Error(t, target.ImportRelational(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), wrong, transferDirectory(t), restrictImportedFixture))
	require.ErrorContains(t, target.ImportRelational(t.Context(), "", snapshot.Snapshot, parent, nil), "restriction callback")
	held, err := target.Conn(t.Context())
	require.NoError(t, err)
	defer held.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, target.ImportRelational(ctx, filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), restrictImportedFixture), context.DeadlineExceeded)
}

func TestRelationalTransferMySQLPendingIntent(t *testing.T) {
	if os.Getenv("TEST_MYSQL_DSN") == "" {
		t.Skip("TEST_MYSQL_DSN is required")
	}
	config := isolatedContractConfig(t, Config{Type: TypeMySQL, MySQL: MySQLConfig{DSN: os.Getenv("TEST_MYSQL_DSN")}})
	db := transferDB(t, config)
	_, err := db.ExecContext(t.Context(), "INSERT INTO schema_migration_attempts VALUES('unknown',?)", strings.Repeat("1", 64))
	require.NoError(t, err)
	result, err := db.SnapshotRelational(t.Context(), filepath.Join(transferDirectory(t), "snapshot"))
	require.ErrorIs(t, err, ErrMigrationRecoveryRequired)
	require.False(t, result.Published)
}

func relationalFixtureRows(t *testing.T, db *DB, table relationalTable) [][]string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT "+table.columnNames()+" FROM "+table.name+" ORDER BY "+table.columnNames())
	require.NoError(t, err)
	defer rows.Close()
	result := [][]string{}
	for rows.Next() {
		values := make([]string, len(table.columns))
		pointers := make([]any, len(values))
		for i := range pointers {
			pointers[i] = &values[i]
		}
		require.NoError(t, rows.Scan(pointers...))
		result = append(result, values)
	}
	require.NoError(t, rows.Err())
	return result
}

func TestRelationalTransferPreservesOneSnapshotDuringWrites(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			db := transferDB(t, config)
			_, err := db.ExecContext(t.Context(), "INSERT INTO users VALUES('user','subject',0,'{}'); INSERT INTO teams VALUES('team',0,'{}')")
			require.NoError(t, err)
			writer, err := Open(config)
			require.NoError(t, err)
			defer writer.Close()
			stopped := make(chan struct{})
			finished := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				for revision := 1; ; revision++ {
					select {
					case <-stopped:
						finished <- nil
						return
					default:
					}
					tx, err := writer.BeginTx(t.Context(), nil)
					if err != nil {
						finished <- err
						return
					}
					_, first := tx.ExecContext(t.Context(), writer.Bind("UPDATE users SET revision=?"), revision)
					_, second := tx.ExecContext(t.Context(), writer.Bind("UPDATE teams SET revision=?"), revision)
					if err := errors.Join(first, second); err != nil {
						_ = tx.Rollback()
						finished <- err
						return
					}
					if err := tx.Commit(); err != nil {
						finished <- err
						return
					}
				}
			}()
			<-started
			defer func() { close(stopped); require.NoError(t, <-finished) }()
			for i := range 3 {
				parent := transferDirectory(t)
				snapshot := filepath.Join(parent, "snapshot")
				_, err := db.SnapshotRelational(t.Context(), snapshot)
				require.NoError(t, err)
				image := openSnapshotFixture(t, filepath.Join(snapshot, "starport.db"))
				var user, team int64
				require.NoError(t, image.QueryRowContext(t.Context(), "SELECT users.revision,teams.revision FROM users CROSS JOIN teams").Scan(&user, &team))
				require.Equal(t, user, team, "snapshot %d", i)
			}
		})
	}
}

func TestRelationalTransferMySQLIncompatibleKeysRollBack(t *testing.T) {
	address := os.Getenv("TEST_MYSQL_DSN")
	if address == "" {
		t.Skip("TEST_MYSQL_DSN is required")
	}
	source, parent := sqliteSnapshotFixture(t)
	// MySQL's default collation cannot represent these two distinct SQLite keys.
	_, err := source.ExecContext(t.Context(), "INSERT INTO users VALUES('a','first',1,'{}'),('A','second',1,'{}')")
	require.NoError(t, err)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	config := isolatedContractConfig(t, Config{Type: TypeMySQL, MySQL: MySQLConfig{DSN: address}})
	target := transferDB(t, config)
	require.Error(t, target.ImportRelational(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), restrictImportedFixture))
	var count int
	require.NoError(t, target.QueryRowContext(t.Context(), "SELECT count(*) FROM users").Scan(&count))
	require.Zero(t, count)
}

func TestRelationalTransferRejectsTimestampPrecisionLoss(t *testing.T) {
	source, parent := sqliteSnapshotFixture(t)
	_, err := source.ExecContext(t.Context(), "UPDATE schema_migrations SET applied_at=? WHERE name='0001_baseline.sql'", time.Date(2026, 9, 28, 12, 0, 0, 123456789, time.UTC))
	require.NoError(t, err)
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		if name == TypeSQLite {
			continue
		}
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			require.ErrorContains(t, target.ImportRelational(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), restrictImportedFixture), "sub-microsecond")
			require.NoError(t, validateTransferMigrations(t.Context(), target, target.dialect))
		})
	}
}

func TestRelationalTransferRestoresMySQLSessionSettings(t *testing.T) {
	address := os.Getenv("TEST_MYSQL_DSN")
	if address == "" {
		t.Skip("TEST_MYSQL_DSN is required")
	}
	config := isolatedContractConfig(t, Config{Type: TypeMySQL, MySQL: MySQLConfig{DSN: address}})
	db := transferDB(t, config)
	db.SetMaxOpenConns(1)
	_, err := db.ExecContext(t.Context(), "SET SESSION time_zone='+02:00', SESSION sql_mode='NO_ENGINE_SUBSTITUTION', SESSION information_schema_stats_expiry=86400")
	require.NoError(t, err)
	parent := transferDirectory(t)
	for _, failure := range []bool{false, true} {
		if failure {
			_, err := db.ExecContext(t.Context(), "CREATE TABLE unknown_data(id INTEGER) ENGINE=InnoDB")
			require.NoError(t, err)
		}
		_, err := db.SnapshotRelational(t.Context(), filepath.Join(parent, strings.ToLower(fmt.Sprint(failure))))
		if failure {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
		var zone, mode string
		var expiry int
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT @@session.time_zone,@@session.sql_mode,@@session.information_schema_stats_expiry").Scan(&zone, &mode, &expiry))
		require.Equal(t, "+02:00", zone)
		require.Equal(t, "NO_ENGINE_SUBSTITUTION", mode)
		require.Equal(t, 86400, expiry)
	}
}

func TestRelationalTransferPreservesExhaustedAuditCounter(t *testing.T) {
	source, parent := sqliteSnapshotFixture(t)
	require.NoError(t, restoreAuditHighWater(t.Context(), source, source, TypeSQLite, 9223372036854775807))
	snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(parent, "snapshot"))
	require.NoError(t, err)
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			target := transferDB(t, config)
			err := target.ImportRelational(t.Context(), filepath.Join(parent, "snapshot", "starport.db"), snapshot.Snapshot, transferDirectory(t), restrictImportedFixture)
			if name == TypeMySQL {
				require.ErrorIs(t, err, ErrAuditCounterUnrepresentable)
				require.NoError(t, validateTransferMigrations(t.Context(), target, target.dialect))
				var count int
				require.NoError(t, target.QueryRowContext(t.Context(), "SELECT count(*) FROM audit_log").Scan(&count))
				require.Zero(t, count)
				return
			}
			require.NoError(t, err)
			// A second export exercises each engine's exhausted native counter read.
			nextParent := transferDirectory(t)
			_, err = target.SnapshotRelational(t.Context(), filepath.Join(nextParent, "snapshot"))
			require.NoError(t, err)
			image := openSnapshotFixture(t, filepath.Join(nextParent, "snapshot", "starport.db"))
			var high int64
			require.NoError(t, image.QueryRowContext(t.Context(), "SELECT seq FROM sqlite_sequence WHERE name='audit_log'").Scan(&high))
			require.Equal(t, int64(9223372036854775807), high)
			_, err = target.ExecContext(t.Context(), "INSERT INTO audit_log(occurred_at,actor,action,subject,outcome,request_id) VALUES('now','operator','after','deployment','ok','new-request')")
			require.Error(t, err)
			var count int
			require.NoError(t, target.QueryRowContext(t.Context(), "SELECT count(*) FROM audit_log").Scan(&count))
			require.Zero(t, count)
		})
	}
}
