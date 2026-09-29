package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// CloseBackupBoundary closes admission approval without starting the gateway.
// Operators must separately stop and fence every writer before capture.
func CloseBackupBoundary(ctx context.Context, cfg *config.Config) (result recovery.Record, resultErr error) {
	db, err := openBackupSQL(cfg)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	witness, err := recovery.New(db)
	if err != nil {
		return result, err
	}
	current, err := witness.Current(ctx, cfg.EffectivePaths().DeploymentID)
	if errors.Is(err, recovery.ErrClosed) {
		return witness.Initialize(ctx, cfg.EffectivePaths().DeploymentID)
	}
	if err != nil {
		return result, err
	}
	if !current.Open {
		return current, nil
	}
	return witness.Close(ctx, current)
}

// CaptureBackup captures the configured stopped deployment without source refresh.
// It does not approve restoration or prove external fencing and later history.
func CaptureBackup(ctx context.Context, cfg *config.Config, request recovery.CaptureRequest) (result recovery.CaptureResult, resultErr error) {
	if err := request.Validate(); err != nil {
		return result, err
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return result, err
	}
	inventory, err := cfg.CollectBackupInventory(ctx, request.Build, request.EntryLimit)
	if err != nil {
		return result, err
	}
	db, err := openBackupSQL(cfg)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	witness, err := recovery.New(db)
	if err != nil {
		return result, err
	}
	boundary, err := witness.Current(ctx, inventory.DeploymentID)
	if err != nil {
		return result, err
	}
	if boundary.Open {
		return result, recovery.ErrClosed
	}
	source, err := storage.OpenSnapshotSource(ctx, cfg.RuntimeStorage())
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, source.Close()) }()
	if cfg.Files.SelectedBackend() == config.BlobBackendFilesystem {
		info, err := os.Lstat(cfg.Files.Path)
		if err != nil {
			return result, err
		}
		if !info.IsDir() {
			return result, errors.New("backup requires an existing blob directory")
		}
	}
	blobs, err := openBlob(ctx, cfg.Files)
	if err != nil {
		return result, err
	}
	file, cleanup, err := stageBackupInventory(ctx, filepath.Dir(request.Destination), inventory)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	files := []recovery.BundleFile{file}
	for _, selected := range inventory.Files {
		files = append(files, recovery.BundleFile{ID: selected.ArtifactID, Path: selected.Source, ExpectedSHA256: selected.ExpectedSHA256})
	}
	manifest, err := recovery.BackupBundle(ctx, request.Destination, recovery.BundleSources{KV: source, SQL: db, Blobs: blobs, Files: files, Encryption: encryption}, recovery.BundleRequest{
		OperationID: request.OperationID, Build: request.Build, Boundary: boundary, FencingEvidence: request.FencingEvidence, KeyReference: request.KeyReference, ExternalRequirements: inventory.Requirements,
	})
	if err != nil {
		return result, err
	}
	digest, err := manifest.Digest()
	if err != nil {
		return result, err
	}
	_, references, err := recovery.InspectBundleReferences(ctx, request.Destination, digest, filepath.Dir(request.Destination), encryption, catalog.InspectCapturedCatalog)
	if err != nil {
		return result, err
	}
	return recovery.CaptureResult{Directory: request.Destination, ManifestSHA256: digest, DeploymentID: inventory.DeploymentID, RecoveryEpoch: boundary.Epoch, Artifacts: len(manifest.Artifacts), References: references}, nil
}

// VerifyBackup checks captured bytes and selected-key access without opening live stores.
func VerifyBackup(ctx context.Context, cfg *config.Config, request recovery.VerifyRequest) (recovery.CaptureResult, error) {
	if err := request.Validate(); err != nil {
		return recovery.CaptureResult{}, err
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return recovery.CaptureResult{}, err
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	manifest, references, err := recovery.InspectBundleReferences(ctx, request.Directory, request.ManifestSHA256, scratch, encryption, catalog.InspectCapturedCatalog)
	if err != nil {
		return recovery.CaptureResult{}, err
	}
	return recovery.CaptureResult{Directory: request.Directory, ManifestSHA256: request.ManifestSHA256, DeploymentID: manifest.Request.Boundary.DeploymentID, RecoveryEpoch: manifest.Request.Boundary.Epoch, Artifacts: len(manifest.Artifacts), References: references}, nil
}

func backupEncryption(cfg *config.Config) (*credentials.EncryptionService, error) {
	if cfg == nil || strings.TrimSpace(cfg.Security.MasterKey) == "" {
		return nil, errors.New("backup requires the configured encryption key")
	}
	key := []byte(cfg.Security.MasterKey)
	if len(key) < 32 {
		key = credentials.DeriveKeyFromPassword(cfg.Security.MasterKey)
	}
	return credentials.NewEncryptionService(key)
}

func openBackupSQL(cfg *config.Config) (*sqlstore.DB, error) {
	if cfg == nil || cfg.RuntimeStorage().Badger.InMemory {
		return nil, errors.New("backup requires persistent deployment configuration")
	}
	selected := cfg.Storage.RuntimeSQL()
	if selected.Type == sqlstore.TypeSQLite {
		info, err := os.Lstat(selected.SQLite.Path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("backup requires an existing relational database")
		}
	}
	return sqlstore.Open(selected)
}

func stageBackupInventory(ctx context.Context, parentPath string, inventory config.BackupInventory) (result recovery.BundleFile, cleanup func() error, resultErr error) {
	body, err := json.Marshal(inventory)
	if err != nil {
		return result, nil, err
	}
	if len(body) > 16<<20 {
		return result, nil, errors.New("backup file inventory exceeds its size limit")
	}
	parent, err := productfiles.ExistingDirectory(parentPath)
	if err != nil {
		return result, nil, err
	}
	root, err := parent.Open()
	if err != nil {
		return result, nil, err
	}
	name := ".backup-inventory-" + rand.Text()
	directory, err := parent.CreateChild(name)
	if err != nil {
		return result, nil, errors.Join(err, root.Close())
	}
	info, err := root.Lstat(name)
	if err != nil {
		return result, nil, errors.Join(err, root.Close())
	}
	cleanup = func() error {
		current, err := root.Lstat(name)
		if err == nil {
			if !os.SameFile(info, current) {
				err = errors.New("backup inventory staging directory changed")
			} else {
				err = errors.Join(root.RemoveAll(name), productfiles.SyncDirectory(root))
			}
		}
		return errors.Join(err, root.Close())
	}
	if err := directory.CompareAndPublish(ctx, "inventory.json", nil, body); err != nil {
		return result, nil, errors.Join(err, cleanup())
	}
	digest := sha256.Sum256(body)
	return recovery.BundleFile{ID: "inventory.json", Path: filepath.Join(parentPath, name, "inventory.json"), ExpectedSHA256: hex.EncodeToString(digest[:])}, cleanup, nil
}
