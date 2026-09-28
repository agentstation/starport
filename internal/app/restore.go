package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// PrepareBackup restores into the configured isolated targets without starting the gateway.
// The operator must stop and fence all writers. Preparation does not approve activation.
func PrepareBackup(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest) (result recovery.PrepareResult, resultErr error) {
	if err := request.Validate(); err != nil {
		return result, err
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return result, err
	}
	paths, err := restoreTargetPaths(cfg, request)
	if err != nil {
		return result, err
	}
	source, err := recovery.InspectRestoreSource(ctx, request.VerifyRequest, encryption)
	if err != nil {
		return result, err
	}
	if cfg.EffectivePaths().DeploymentID != source.DeploymentID() {
		return result, errors.New("restore target deployment ID differs from the verified backup")
	}
	filePlan, err := planBackupFiles(ctx, cfg, source)
	if err != nil {
		return result, err
	}
	// Create private target parents only after the complete source passes verification.
	for _, path := range paths {
		if _, err := productfiles.NewDirectory(filepath.Dir(path)); err != nil {
			return result, err
		}
	}
	kvConfig := cfg.RuntimeStorage()
	if kvConfig.Type == storage.StorageTypeBadger {
		if _, err := productfiles.NewDirectory(kvConfig.Badger.Path); err != nil {
			return result, err
		}
	}
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	if err := db.PrepareImportSchema(ctx); err != nil {
		return result, err
	}
	store, transfer, err := storage.OpenImportTarget(ctx, kvConfig)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, store.Close()) }()
	blobs, err := restoreBlobTarget(ctx, cfg.Files)
	if err != nil {
		return result, err
	}
	prepared, err := source.Prepare(ctx, recovery.BundleTargets{KV: transfer, SQL: db, Blobs: blobs, FilesDirectory: request.FilesDirectory}, request.Operation)
	if err != nil {
		return result, err
	}
	return recovery.PrepareResult{Prepared: prepared, FilesDirectory: request.FilesDirectory, References: source.References(), FilePlan: filePlan}, nil
}

func restoreTargetPaths(cfg *config.Config, request recovery.PrepareRequest) ([]string, error) {
	if cfg == nil {
		return nil, errors.New("restore requires target configuration")
	}
	kv := cfg.RuntimeStorage()
	if err := kv.Validate(); err != nil {
		return nil, err
	}
	sql := cfg.Storage.RuntimeSQL()
	if err := sql.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.Files.Validate(); err != nil {
		return nil, err
	}
	paths := []string{request.FilesDirectory}
	if kv.Type == storage.StorageTypeBadger {
		if kv.Badger.InMemory {
			return nil, errors.New("restore requires persistent KV storage")
		}
		paths = append(paths, kv.Badger.Path)
	}
	if sql.Type == sqlstore.TypeSQLite {
		if sql.SQLite.Path == "" {
			return nil, errors.New("restore requires persistent SQL storage")
		}
		paths = append(paths, sql.SQLite.Path)
		if info, err := os.Lstat(sql.SQLite.Path); err == nil {
			if !info.Mode().IsRegular() {
				return nil, errors.New("restore requires a regular SQL target file")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if cfg.Files.SelectedBackend() == config.BlobBackendFilesystem {
		paths = append(paths, cfg.Files.Path)
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	if err := recovery.CheckRestoreDestinations(request.Directory, scratch, paths...); err != nil {
		return nil, err
	}
	return paths, nil
}

func restoreBlobTarget(ctx context.Context, cfg config.FilesConfig) (blob.RestoreTarget, error) {
	if cfg.SelectedBackend() == config.BlobBackendFilesystem {
		return blob.FilesystemRestoreTarget(cfg.Path)
	}
	store, err := openObjectBlob(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return blob.ObjectRestoreTarget(store)
}

func planBackupFiles(ctx context.Context, cfg *config.Config, source *recovery.RestoreSource) ([]recovery.FileDisposition, error) {
	body, err := source.SelectedFile(ctx, "inventory.json", 16<<20)
	if err != nil {
		return nil, err
	}
	var inventory config.BackupInventory
	if err := json.Unmarshal(body, &inventory, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	hashes := source.SelectedFileHashes()
	delete(hashes, "inventory.json")
	plan, err := cfg.PlanRestoreFiles(inventory, hashes)
	if err != nil {
		return nil, err
	}
	result := make([]recovery.FileDisposition, 0, len(plan))
	for _, file := range plan {
		result = append(result, recovery.FileDisposition{ArtifactID: file.ArtifactID, Role: file.Role, Relative: file.Relative, SHA256: file.SHA256, Destination: file.Destination, Action: file.Action, Reason: file.Reason})
	}
	return result, nil
}
