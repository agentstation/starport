package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
)

// WriteImportedHistory writes the final-only history package that binds a verified backup to the fenced target.
// The KV expectation comes from the backup and the SQL expectation from the target. No target store changes.
// The command starts no gateway or background worker and grants no admission.
func WriteImportedHistory(ctx context.Context, cfg *config.Config, request recovery.WriteHistoryRequest) (recovery.HistoryWriteReport, error) {
	if ctx == nil {
		return recovery.HistoryWriteReport{}, errors.New("history writing requires a context")
	}
	if err := request.Validate(); err != nil {
		return recovery.HistoryWriteReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return recovery.HistoryWriteReport{}, err
	}
	if err := validateWriteHistoryTargets(cfg, request); err != nil {
		return recovery.HistoryWriteReport{}, err
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return recovery.HistoryWriteReport{}, err
	}
	source, err := recovery.InspectRestoreSource(ctx, request.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	if err != nil {
		return recovery.HistoryWriteReport{}, err
	}
	if cfg.EffectivePaths().DeploymentID != source.DeploymentID() {
		return recovery.HistoryWriteReport{}, errors.Join(recovery.ErrConflict, errors.New("history writing deployment differs from the verified backup"))
	}
	return writeConfiguredHistory(ctx, cfg, source, request)
}

// validateWriteHistoryTargets keeps the package apart from the bundle, scratch, and every local target path.
func validateWriteHistoryTargets(cfg *config.Config, request recovery.WriteHistoryRequest) error {
	if _, err := restoreTargetPaths(cfg, recovery.PrepareRequest{VerifyRequest: request.VerifyRequest, FilesDirectory: request.History.Directory}); err != nil {
		return err
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	if err := recovery.CheckInspectionScratch(request.Directory, scratch); err != nil {
		return err
	}
	if err := recovery.CheckInspectionScratch(request.History.Directory, scratch); err != nil {
		return err
	}
	for _, path := range []string{request.History.Directory, scratch} {
		if _, err := productfiles.ExistingDirectory(path); err != nil {
			return err
		}
	}
	return validateExistingImportedTargets(cfg, request.ValkeyIncarnation)
}

func writeConfiguredHistory(ctx context.Context, cfg *config.Config, source *recovery.RestoreSource, request recovery.WriteHistoryRequest) (report recovery.HistoryWriteReport, resultErr error) {
	defer func() {
		if resultErr != nil {
			report = recovery.HistoryWriteReport{}
		}
	}()
	db, err := openBackupSQL(cfg)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	blobs, err := restoreBlobTarget(ctx, cfg.Files)
	if err != nil {
		return report, err
	}
	target, err := configuredRecoveryTarget(ctx, cfg, db, blobs, request.ValkeyIncarnation)
	if err != nil || request.ExpectedTargetSHA256 != "" && target != request.ExpectedTargetSHA256 {
		return report, errors.Join(recovery.ErrConflict, err)
	}
	expected, err := captureTargetSQLRevision(ctx, db)
	if err != nil {
		return report, err
	}
	history := request.History
	history.TargetSHA256, history.SQLExpected = target, expected
	written, err := source.WriteFinalHistory(ctx, history)
	if err != nil {
		return report, err
	}
	// Fenced writers keep both bindings unchanged. A change means the package describes another target state.
	after, err := configuredRecoveryTarget(ctx, cfg, db, blobs, request.ValkeyIncarnation)
	if err != nil || after != target {
		return report, errors.Join(recovery.ErrConflict, err)
	}
	current, err := captureTargetSQLRevision(ctx, db)
	if err != nil || !sameSQLRevision(expected, current) {
		return report, errors.Join(recovery.ErrConflict, err)
	}
	return recovery.HistoryWriteReport{Directory: history.Directory, HistorySHA256: written.Digest(), TargetSHA256: target, DeclaredSteps: written.StepCount()}, nil
}

func captureTargetSQLRevision(ctx context.Context, db *sqlstore.DB) (_ *revision.Stamp, resultErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	return revision.CaptureSQLRecovery(ctx, db, conn)
}

func sameSQLRevision(a, b *revision.Stamp) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
