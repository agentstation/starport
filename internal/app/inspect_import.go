package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// InspectImportedBackup checks the configured closed import against explicit retained identities.
// It never prepares targets, starts a gateway, or approves admission. Every writer must stay fenced.
func InspectImportedBackup(ctx context.Context, cfg *config.Config, request recovery.InspectImportRequest) (recovery.ImportInspectionResult, error) {
	if ctx == nil {
		return recovery.ImportInspectionResult{}, errors.New("import inspection requires a context")
	}
	if err := request.Validate(); err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	if err := validateInspectionTargets(cfg, request); err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	source, err := recovery.InspectRestoreSource(ctx, request.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	if err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	if source.DeploymentID() != cfg.EffectivePaths().DeploymentID || source.DeploymentID() != request.ExpectedBoundary.DeploymentID {
		return recovery.ImportInspectionResult{}, errors.New("import inspection deployment differs from the verified backup or configured target")
	}
	identity, err := source.ImportIdentity(request.Operation)
	if err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	result, err := inspectConfiguredImport(ctx, cfg, request, identity, encryption, source.CapturedBoundary())
	if err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	encoded, err := json.Marshal(result, json.Deterministic(true))
	if err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	directory, err := productfiles.ExistingDirectory(request.Destination)
	if err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	if err := directory.CompareAndPublish(ctx, "inspection.json", nil, encoded); err != nil {
		return recovery.ImportInspectionResult{}, err
	}
	return result, nil
}

func validateInspectionTargets(cfg *config.Config, request recovery.InspectImportRequest) error {
	_, err := restoreTargetPaths(cfg, recovery.PrepareRequest{VerifyRequest: request.VerifyRequest, FilesDirectory: request.Destination})
	if err != nil {
		return err
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	if err := recovery.CheckInspectionScratch(request.Directory, scratch); err != nil {
		return err
	}
	if _, err := productfiles.ExistingDirectory(scratch); err != nil {
		return err
	}
	if _, err := productfiles.ExistingDirectory(filepath.Dir(request.Destination)); err != nil {
		return err
	}
	if _, err := os.Lstat(request.Destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("import inspection requires a new output directory")
	}
	return validateExistingImportedTargets(cfg, request.ValkeyIncarnation)
}

func validateExistingImportedTargets(cfg *config.Config, incarnation string) error {
	kv := cfg.RuntimeStorage()
	if kv.Type == storage.StorageTypeBadger {
		if incarnation != "" {
			return errors.New("import inspection for Badger does not accept a Valkey incarnation")
		}
		if _, err := productfiles.ExistingDirectory(kv.Badger.Path); err != nil {
			return err
		}
		info, err := os.Lstat(filepath.Join(kv.Badger.Path, "MANIFEST"))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("import inspection requires an existing Badger manifest")
		}
	} else if incarnation == "" {
		return errors.New("import inspection for Valkey requires an explicit serving process incarnation")
	}
	selectedSQL := cfg.Storage.RuntimeSQL()
	if selectedSQL.Type == sqlstore.TypeSQLite {
		info, err := os.Lstat(selectedSQL.SQLite.Path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("import inspection requires an existing SQL database")
		}
	}
	if cfg.Files.SelectedBackend() == config.BlobBackendFilesystem {
		if _, err := productfiles.ExistingDirectory(cfg.Files.Path); err != nil {
			return err
		}
	}
	return nil
}

func inspectConfiguredImport(ctx context.Context, cfg *config.Config, request recovery.InspectImportRequest, identity recovery.PreparedImportIdentity, encryption *credentials.EncryptionService, capturedBoundary recovery.Record) (result recovery.ImportInspectionResult, resultErr error) {
	defer func() {
		if resultErr != nil {
			result = recovery.ImportInspectionResult{}
		}
	}()
	db, err := openBackupSQL(cfg)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	witness, err := recovery.New(db)
	if err != nil {
		return result, err
	}
	current, err := witness.Current(ctx, request.ExpectedBoundary.DeploymentID)
	if err != nil || current != request.ExpectedBoundary {
		return result, errors.Join(recovery.ErrConflict, err)
	}
	store, transfer, err := storage.OpenImportTarget(ctx, cfg.RuntimeStorage())
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, store.Close()) }()
	if request.ValkeyIncarnation != "" {
		transfer, err = storage.OpenRecordTransfer(ctx, store, request.ValkeyIncarnation)
		if err != nil {
			return result, err
		}
	}
	kvInspector, ok := transfer.(storage.ImportInspector)
	if !ok {
		return result, errors.New("configured KV target does not support closed import inspection")
	}
	blobs, err := restoreBlobTarget(ctx, cfg.Files)
	if err != nil {
		return result, err
	}
	target, err := configuredRecoveryTarget(ctx, cfg, db, blobs, request.ValkeyIncarnation)
	if err != nil {
		return result, err
	}
	blobInspector, ok := blobs.(blob.ImportInspector)
	if !ok {
		return result, errors.New("configured blob target does not support closed import inspection")
	}
	native := recovery.ImportedReferenceRequest{Boundary: request.ExpectedBoundary, CapturedAt: time.Now().UTC(), KVClaim: identity.KVClaim, KVPosition: request.KVPosition, SQLOriginal: identity.SQLOriginal, SQLIdentity: identity.SQL, SQLPosition: request.SQLPosition, BlobOperation: identity.ComponentOperation, BlobPosition: request.BlobPosition, BlobOriginal: identity.BlobOriginal}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	// Imported catalog records retain the source identity until coordinated topology preparation.
	// Native guards still check the exact target boundary and replay positions.
	inspectCatalog := func(ctx context.Context, view *recovery.KVSnapshotView, _ recovery.Record) error {
		return catalog.InspectCapturedCatalog(ctx, view, capturedBoundary)
	}
	checked, err := recovery.InspectImportedReferences(ctx, recovery.ImportedReferenceSources{KV: kvInspector, SQL: db, Blobs: blobInspector}, native, request.Destination, scratch, encryption, inspectCatalog)
	if err != nil {
		return result, err
	}
	current, err = witness.Current(ctx, request.ExpectedBoundary.DeploymentID)
	if err != nil || current != request.ExpectedBoundary {
		return result, errors.Join(recovery.ErrConflict, err)
	}
	after, err := configuredRecoveryTarget(ctx, cfg, db, blobs, request.ValkeyIncarnation)
	if err != nil || target != after {
		return result, errors.Join(recovery.ErrConflict, err)
	}
	return recovery.ImportInspectionResult{Directory: request.Destination, ManifestSHA256: request.ManifestSHA256, TargetSHA256: target, Operation: request.Operation, ValkeyIncarnation: request.ValkeyIncarnation, Request: native, Inspection: checked}, nil
}
