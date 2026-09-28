package sqlstore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func mysqlReconciliationFixture(t *testing.T) (*DB, fstest.MapFS, MigrationReconciliation) {
	t.Helper()
	address := os.Getenv("TEST_MYSQL_DSN")
	if address == "" {
		t.Skip("TEST_MYSQL_DSN is required")
	}
	config := isolatedContractConfig(t, Config{Type: TypeMySQL, MySQL: MySQLConfig{DSN: address}})
	db, err := Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	source := fstest.MapFS{"migrations/mysql/0001_probe.sql": {Data: []byte("CREATE TABLE reconcile_probe (id INTEGER PRIMARY KEY); INSERT INTO reconcile_probe VALUES(1);")}}
	decision := MigrationReconciliation{OperationID: "repair-1", Name: "0001_probe.sql", Digest: fmt.Sprintf("%x", sha256.Sum256(source["migrations/mysql/0001_probe.sql"].Data)), Outcome: "applied", Actor: "operator", EvidenceSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("inspection proof")))}
	// Reproduce a crash after DDL committed but before the migration receipt.
	_, err = db.ExecContext(t.Context(), db.schemaMigrationsDDL())
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "CREATE TABLE schema_migration_attempts(name VARCHAR(191) PRIMARY KEY,digest VARCHAR(64) NOT NULL)")
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO schema_migration_attempts VALUES(?,?)", decision.Name, decision.Digest)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), string(source["migrations/mysql/0001_probe.sql"].Data))
	require.NoError(t, err)
	require.ErrorIs(t, db.migrate(t.Context(), source), ErrMigrationRecoveryRequired)
	return db, source, decision
}

func TestMySQLMigrationReconciliationAppliedDoesNotRepeatDDL(t *testing.T) {
	db, source, decision := mysqlReconciliationFixture(t)
	require.NoError(t, db.reconcileMySQLMigration(t.Context(), decision, source))
	require.NoError(t, db.reconcileMySQLMigration(t.Context(), decision, source))
	require.NoError(t, db.migrate(t.Context(), source))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM reconcile_probe").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migration_attempts").Scan(&count))
	require.Zero(t, count)
	var actual MigrationReconciliation
	var recorded string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT operation_id,name,digest,outcome,actor,evidence_sha256,CAST(recorded_at AS CHAR) FROM schema_migration_reconciliations").Scan(&actual.OperationID, &actual.Name, &actual.Digest, &actual.Outcome, &actual.Actor, &actual.EvidenceSHA256, &recorded))
	require.Equal(t, decision, actual)
	require.NotEmpty(t, recorded)
	changed := decision
	changed.Outcome = "reverted"
	require.ErrorContains(t, db.reconcileMySQLMigration(t.Context(), changed, source), "conflicts")
}

func TestMySQLMigrationReconciliationRevertedAllowsExplicitRetry(t *testing.T) {
	db, source, decision := mysqlReconciliationFixture(t)
	// The administrator repairs the schema before declaring the reverted outcome.
	_, err := db.ExecContext(t.Context(), "DROP TABLE reconcile_probe")
	require.NoError(t, err)
	decision.Outcome = "reverted"
	require.NoError(t, db.reconcileMySQLMigration(t.Context(), decision, source))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.migrate(t.Context(), source))
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM reconcile_probe").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migration_reconciliations WHERE outcome='reverted'").Scan(&count))
	require.Equal(t, 1, count)
}

func TestMySQLMigrationReconciliationRejectsUnboundEvidence(t *testing.T) {
	db, source, decision := mysqlReconciliationFixture(t)
	for _, scenario := range []string{"source-digest", "attempt-digest", "actor", "evidence", "unknown-migration", "outcome", "missing-attempt"} {
		t.Run(scenario, func(t *testing.T) {
			d := decision
			switch scenario {
			case "source-digest":
				d.Digest = strings.Repeat("0", 64)
			case "attempt-digest":
				_, err := db.ExecContext(t.Context(), "UPDATE schema_migration_attempts SET digest=?", strings.Repeat("0", 64))
				require.NoError(t, err)
				defer func() {
					_, err := db.ExecContext(t.Context(), "UPDATE schema_migration_attempts SET digest=?", decision.Digest)
					require.NoError(t, err)
				}()
			case "actor":
				d.Actor = ""
			case "evidence":
				d.EvidenceSHA256 = ""
			case "unknown-migration":
				d.Name = "9999_unknown.sql"
			case "outcome":
				d.Outcome = "retry"
			case "missing-attempt":
				_, err := db.ExecContext(t.Context(), "DELETE FROM schema_migration_attempts")
				require.NoError(t, err)
				defer func() {
					_, err := db.ExecContext(t.Context(), "INSERT INTO schema_migration_attempts VALUES(?,?)", decision.Name, decision.Digest)
					require.NoError(t, err)
				}()
			}
			require.Error(t, db.reconcileMySQLMigration(t.Context(), d, source))
			var count int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&count))
			require.Zero(t, count)
		})
	}
	require.ErrorIs(t, db.migrate(t.Context(), source), ErrMigrationRecoveryRequired)
}

func TestMySQLMigrationReconciliationSerializesConflictingOperators(t *testing.T) {
	db, source, decision := mysqlReconciliationFixture(t)
	var wg sync.WaitGroup
	outcomes := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			d := decision
			d.Actor = fmt.Sprintf("operator-%d", i)
			outcomes <- db.reconcileMySQLMigration(t.Context(), d, source)
		})
	}
	wg.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else {
			require.ErrorContains(t, err, "conflicts")
		}
	}
	require.Equal(t, 1, successes)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migration_reconciliations").Scan(&count))
	require.Equal(t, 1, count)
}

func TestMySQLMigrationReconciliationRollsBackReceiptFailure(t *testing.T) {
	db, source, decision := mysqlReconciliationFixture(t)
	// Force the audit write to fail after the completion INSERT in the same transaction.
	_, err := db.ExecContext(t.Context(), `CREATE TABLE schema_migration_reconciliations (
 operation_id VARCHAR(191) PRIMARY KEY,name VARCHAR(191),digest VARCHAR(64),outcome VARCHAR(16),actor VARCHAR(191),evidence_sha256 VARCHAR(64),
 CONSTRAINT reject_reconciliation CHECK (operation_id='unusable')
 )`)
	require.NoError(t, err)
	require.Error(t, db.reconcileMySQLMigration(t.Context(), decision, source))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migration_attempts").Scan(&count))
	require.Equal(t, 1, count)
	require.ErrorIs(t, db.migrate(t.Context(), source), ErrMigrationRecoveryRequired)
}

func TestMySQLMigrationReconciliationRequiresTransactionalRecords(t *testing.T) {
	for _, table := range []string{"schema_migrations", "schema_migration_attempts", "schema_migration_reconciliations"} {
		t.Run(table, func(t *testing.T) {
			db, source, decision := mysqlReconciliationFixture(t)
			if table == "schema_migration_reconciliations" {
				_, err := db.ExecContext(t.Context(), `CREATE TABLE schema_migration_reconciliations (
     operation_id VARCHAR(191) PRIMARY KEY,name VARCHAR(191),digest VARCHAR(64),outcome VARCHAR(16),actor VARCHAR(191),evidence_sha256 VARCHAR(64)
    ) ENGINE=MyISAM`)
				require.NoError(t, err)
			} else {
				// Table names come only from the fixed contract roster above.
				_, err := db.ExecContext(t.Context(), "ALTER TABLE "+table+" ENGINE=MyISAM")
				require.NoError(t, err)
			}
			require.ErrorContains(t, db.reconcileMySQLMigration(t.Context(), decision, source), "requires InnoDB")
			var count int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&count))
			require.Zero(t, count)
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migration_attempts").Scan(&count))
			require.Equal(t, 1, count)
			if table != "schema_migration_reconciliations" {
				require.ErrorContains(t, db.migrate(t.Context(), source), "requires InnoDB")
			}
		})
	}
}
