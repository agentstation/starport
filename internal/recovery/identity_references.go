package recovery

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

func inspectIdentityReferences(ctx context.Context, directory, scratch string, manifest BundleManifest, records *KVSnapshotView, report *ReferenceReport) (resultErr error) {
	image, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(directory, bundleSQLFile), manifest.SQL, scratch)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, image.Close()) }()
	boundary := Record{DeploymentID: manifest.Request.Boundary.DeploymentID}
	var opened, bootstrap int
	err = image.QueryRowContext(ctx, `SELECT epoch,gate_open,backend_id,evidence,bootstrap_allowed FROM catalog_recovery WHERE deployment_id=?`, boundary.DeploymentID).Scan(&boundary.Epoch, &opened, &boundary.BackendID, &boundary.Evidence, &bootstrap)
	if err != nil || opened != 0 || bootstrap != 0 || boundary != manifest.Request.Boundary {
		return errors.Join(ErrConflict, err)
	}
	exists := func(ctx context.Context, id string) (bool, error) {
		_, err := account.ReadRecoveryAccount(ctx, records, id)
		if errors.Is(err, account.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	}
	report.Identity, err = identity.VerifyRecoverySnapshot(ctx, image, exists)
	if err != nil {
		return err
	}
	report.AccountTemplates, err = inspectAccountTemplates(ctx, image)
	return err
}

func inspectAccountTemplates(ctx context.Context, image *sqlstore.RelationalSnapshotView) (count int64, resultErr error) {
	rows, err := image.QueryContext(ctx, `SELECT id,revision,CASE WHEN length(CAST(record AS BLOB))<=? THEN record END FROM account_templates ORDER BY id`, account.RecoveryTemplateMaxBytes)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var id string
		var revision uint64
		var data sql.NullString
		if err := rows.Scan(&id, &revision, &data); err != nil {
			return count, err
		}
		if !data.Valid {
			return count, storage.ErrValueTooLarge
		}
		if err := account.VerifyRecoveryTemplate(id, revision, data.String); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}
