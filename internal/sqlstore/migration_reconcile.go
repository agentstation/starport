package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// MigrationReconciliation records an operator's independently verified DDL outcome.
// Applied means all effects exist. Reverted means all effects of the attempt are absent.
// The caller authorizes the administrator and retains the evidence bytes.
type MigrationReconciliation struct {
	OperationID    string
	Name           string
	Digest         string
	Outcome        string
	Actor          string
	EvidenceSHA256 string
}

// ReconcileMySQLMigration records verified repair without executing migration SQL.
// The operator must first establish the declared outcome from the schema and data.
// An exact operation retry is idempotent. Changed decisions require another operation ID.
func (db *DB) ReconcileMySQLMigration(ctx context.Context, decision MigrationReconciliation) error {
	return db.reconcileMySQLMigration(ctx, decision, migrations)
}

func (db *DB) reconcileMySQLMigration(ctx context.Context, decision MigrationReconciliation, fsys fs.FS) (resultErr error) {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	if db.dialect != TypeMySQL {
		return errors.New("migration reconciliation requires MySQL")
	}
	if err := decision.validate(); err != nil {
		return err
	}
	names, err := migrationNames(fsys, db.dialect)
	if err != nil {
		return err
	}
	if !slices.Contains(names, decision.Name) {
		return errors.New("migration reconciliation names an unknown migration")
	}
	body, err := fs.ReadFile(fsys, "migrations/mysql/"+decision.Name)
	if err != nil {
		return err
	}
	if decision.Digest != fmt.Sprintf("%x", sha256.Sum256(body)) {
		return errors.New("migration reconciliation source digest mismatch")
	}
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, owner.close()) }()
	if err := db.validateMigrationHistory(ctx, owner.conn, names); err != nil {
		return err
	}
	if _, err := owner.conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migration_reconciliations (
  operation_id VARCHAR(191) PRIMARY KEY,
  name VARCHAR(191) NOT NULL,
  digest VARCHAR(64) NOT NULL,
  outcome VARCHAR(16) NOT NULL,
  actor VARCHAR(191) NOT NULL,
  evidence_sha256 VARCHAR(64) NOT NULL,
  recorded_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
 ) ENGINE=InnoDB`); err != nil {
		return err
	}
	if err := validateMySQLMigrationEngines(ctx, owner.conn, "schema_migrations", "schema_migration_attempts", "schema_migration_reconciliations"); err != nil {
		return err
	}
	return owner.transaction(ctx, func() error { return db.commitMigrationReconciliation(ctx, owner.conn, decision) })
}

func (d MigrationReconciliation) validate() error {
	for _, value := range []string{d.OperationID, d.Name, d.Actor} {
		if strings.TrimSpace(value) == "" || len(value) > 191 || strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("migration reconciliation requires bounded operation, migration, and actor identifiers")
		}
	}
	if d.Outcome != "applied" && d.Outcome != "reverted" {
		return errors.New("migration reconciliation outcome must be applied or reverted")
	}
	for _, value := range []string{d.Digest, d.EvidenceSHA256} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size || strings.ToLower(value) != value {
			return errors.New("migration reconciliation requires lowercase SHA256 digests")
		}
	}
	return nil
}

func (db *DB) commitMigrationReconciliation(ctx context.Context, conn *sql.Conn, d MigrationReconciliation) error {
	var previous MigrationReconciliation
	err := conn.QueryRowContext(ctx, `SELECT operation_id,name,digest,outcome,actor,evidence_sha256 FROM schema_migration_reconciliations WHERE operation_id=?`, d.OperationID).Scan(&previous.OperationID, &previous.Name, &previous.Digest, &previous.Outcome, &previous.Actor, &previous.EvidenceSHA256)
	if err == nil {
		if previous != d {
			return errors.New("migration reconciliation operation conflicts with its existing receipt")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var pendingDigest string
	if err := conn.QueryRowContext(ctx, "SELECT digest FROM schema_migration_attempts WHERE name=?", d.Name).Scan(&pendingDigest); err != nil {
		return fmt.Errorf("read pending migration: %w", err)
	}
	if pendingDigest != d.Digest {
		return errors.New("migration reconciliation does not match the pending attempt")
	}
	applied, err := db.migrationApplied(ctx, conn, d.Name)
	if err != nil {
		return err
	}
	if applied {
		return errors.New("pending migration already has a completion record")
	}
	if d.Outcome == "applied" {
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations(name) VALUES(?)", d.Name); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO schema_migration_reconciliations(operation_id,name,digest,outcome,actor,evidence_sha256) VALUES(?,?,?,?,?,?)`, d.OperationID, d.Name, d.Digest, d.Outcome, d.Actor, d.EvidenceSHA256); err != nil {
		return err
	}
	deleted, err := conn.ExecContext(ctx, "DELETE FROM schema_migration_attempts WHERE name=? AND digest=?", d.Name, d.Digest)
	if err != nil {
		return err
	}
	count, err := deleted.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("pending migration changed before reconciliation")
	}
	return nil
}
