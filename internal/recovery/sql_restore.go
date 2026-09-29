package recovery

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
)

// SQLRestoreResult identifies restricted SQL state, not permission to start a gateway.
type SQLRestoreResult struct {
	Boundary   Record          `json:"boundary"`
	References ReferenceReport `json:"references"`
}

// PrepareSQLRestore validates the complete bundle before importing relational records.
// All target writers must remain fenced. Independent reconciliation and activation remain required.
func PrepareSQLRestore(ctx context.Context, target *sqlstore.DB, request VerifyRequest, operation string, encryption *credentials.EncryptionService) (result SQLRestoreResult, err error) {
	if target == nil {
		return result, sqlstore.ErrClosed
	}
	if err := request.Validate(); err != nil {
		return result, err
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	manifest, references, err := InspectBundleReferences(ctx, request.Directory, request.ManifestSHA256, scratch, encryption)
	if err != nil {
		return result, err
	}
	return prepareVerifiedSQLRestore(ctx, target, request, operation, scratch, manifest, references)
}

func prepareVerifiedSQLRestore(ctx context.Context, target *sqlstore.DB, request VerifyRequest, operation, scratch string, manifest BundleManifest, references ReferenceReport) (result SQLRestoreResult, err error) {
	original := manifest.Request.Boundary
	identity, expected, err := sqlRestoreIdentity(operation, request.ManifestSHA256, original)
	if err != nil {
		return result, err
	}
	restrict := func(ctx context.Context, conn *sql.Conn) error {
		return restrictRestoredSQL(ctx, target, conn, original, expected)
	}
	if err := target.ImportRelationalOnce(ctx, filepath.Join(request.Directory, bundleSQLFile), manifest.SQL, scratch, identity, restrict); err != nil {
		return result, err
	}
	if err := verifyPreparedSQL(ctx, target, expected); err != nil {
		return result, err
	}
	return SQLRestoreResult{Boundary: expected, References: references}, nil
}

func verifyPreparedSQL(ctx context.Context, target *sqlstore.DB, expected Record) error {
	witness, err := New(target)
	if err != nil {
		return err
	}
	actual, err := witness.Current(ctx, expected.DeploymentID)
	if err != nil || actual != expected {
		return errors.Join(ErrConflict, err)
	}
	if err := verifySQLRestoreRestrictions(ctx, target); err != nil {
		return err
	}
	if err := target.CheckImportBarrier(ctx); !errors.Is(err, sqlstore.ErrImportRestricted) {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func restrictRestoredSQL(ctx context.Context, target *sqlstore.DB, conn *sql.Conn, original, expected Record) error {
	actual := Record{DeploymentID: original.DeploymentID}
	var opened, bootstrap int
	err := conn.QueryRowContext(ctx, target.Bind("SELECT epoch,gate_open,backend_id,evidence,bootstrap_allowed FROM catalog_recovery WHERE deployment_id=?"), original.DeploymentID).Scan(&actual.Epoch, &opened, &actual.BackendID, &actual.Evidence, &bootstrap)
	if err != nil || opened != 0 || bootstrap != 0 || actual != original {
		return errors.Join(ErrConflict, err)
	}
	var invalid int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_recovery WHERE epoch<=0 OR epoch>=9223372036854775807").Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return ErrConflict
	}
	// Every retained deployment gate closes, including records outside the selected deployment.
	for _, query := range []string{"UPDATE catalog_recovery SET epoch=epoch+1,gate_open=0,bootstrap_allowed=0,backend_id='',evidence=''", "UPDATE team_budget_origins SET initialize_allowed=0"} {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, target.Bind("UPDATE catalog_recovery SET evidence=? WHERE deployment_id=?"), expected.Evidence, expected.DeploymentID)
	return err
}

func verifySQLRestoreRestrictions(ctx context.Context, target *sqlstore.DB) error {
	for _, query := range []string{"SELECT COUNT(*) FROM catalog_recovery WHERE gate_open<>0 OR bootstrap_allowed<>0", "SELECT COUNT(*) FROM team_budget_origins WHERE initialize_allowed<>0"} {
		var count int
		if err := target.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrConflict
		}
	}
	return nil
}
