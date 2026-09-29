package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// ApplyImportedHistory accepts independent evidence and replays typed owner transitions.
// The command retains every import barrier and starts no gateway or background worker.
func ApplyImportedHistory(ctx context.Context, cfg *config.Config, request recovery.ApplyHistoryRequest) (recovery.HistoryReplayReport, error) {
	if ctx == nil {
		return recovery.HistoryReplayReport{}, errors.New("history application requires a context")
	}
	if err := request.Validate(); err != nil {
		return recovery.HistoryReplayReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return recovery.HistoryReplayReport{}, err
	}
	if err := validateHistoryTargets(cfg, request); err != nil {
		return recovery.HistoryReplayReport{}, err
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return recovery.HistoryReplayReport{}, err
	}
	source, err := recovery.InspectRestoreSource(ctx, request.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	if err != nil {
		return recovery.HistoryReplayReport{}, err
	}
	if cfg.EffectivePaths().DeploymentID != source.DeploymentID() {
		return recovery.HistoryReplayReport{}, errors.New("history application deployment differs from the verified backup")
	}
	return applyConfiguredHistory(ctx, cfg, source, encryption, request)
}

func validateHistoryTargets(cfg *config.Config, request recovery.ApplyHistoryRequest) error {
	paths, err := restoreTargetPaths(cfg, recovery.PrepareRequest{VerifyRequest: request.VerifyRequest, FilesDirectory: request.JournalDirectory})
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
	if err := recovery.CheckInspectionScratch(request.HistoryDirectory, scratch); err != nil {
		return err
	}
	if err := recovery.CheckRestoreDestinations(request.Directory, scratch, request.HistoryDirectory); err != nil {
		return err
	}
	if err := recovery.CheckRestoreDestinations(request.HistoryDirectory, scratch, paths...); err != nil {
		return err
	}
	for _, path := range []string{request.HistoryDirectory, request.JournalDirectory, scratch} {
		if _, err := productfiles.ExistingDirectory(path); err != nil {
			return err
		}
	}
	return validateExistingImportedTargets(cfg, request.ValkeyIncarnation)
}

func applyConfiguredHistory(ctx context.Context, cfg *config.Config, source *recovery.RestoreSource, encryption *credentials.EncryptionService, request recovery.ApplyHistoryRequest) (report recovery.HistoryReplayReport, resultErr error) {
	defer func() {
		if resultErr != nil {
			report = recovery.HistoryReplayReport{}
		}
	}()
	db, err := openBackupSQL(cfg)
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	witness, err := recovery.New(db)
	if err != nil {
		return report, err
	}
	blobs, err := restoreBlobTarget(ctx, cfg.Files)
	if err != nil {
		return report, err
	}
	target, err := configuredRecoveryTarget(ctx, cfg, db, blobs, request.ValkeyIncarnation)
	if err != nil || target != request.ExpectedTargetSHA256 {
		return report, errors.Join(recovery.ErrConflict, err)
	}
	history, err := source.VerifyHistoryPackage(ctx, recovery.HistoryPackageRequest{Directory: request.HistoryDirectory, ManifestSHA256: request.HistorySHA256, TargetSHA256: target, Operation: request.Operation})
	if err != nil {
		return report, err
	}
	store, transfer, err := storage.OpenImportTarget(ctx, cfg.RuntimeStorage())
	if err != nil {
		return report, err
	}
	defer func() { resultErr = errors.Join(resultErr, store.Close()) }()
	if request.ValkeyIncarnation != "" {
		transfer, err = storage.OpenRecordTransfer(ctx, store, request.ValkeyIncarnation)
		if err != nil {
			return report, err
		}
	}
	kv, ok := transfer.(recovery.HistoryKVTarget)
	if !ok {
		return report, errors.New("configured KV target cannot replay imported history")
	}
	assets, ok := blobs.(recovery.HistoryBlobTarget)
	if !ok {
		return report, errors.New("configured blob target cannot replay imported history")
	}
	accepted, err := witness.AcceptImportedHistory(ctx, source, history, recovery.HistoryAcceptanceRequest{Directory: request.JournalDirectory, Attestation: request.Attestation})
	if err != nil {
		return report, err
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	completed, err := witness.ReplayImportedHistory(ctx, source, accepted, recovery.HistoryReplayTargets{KV: kv, Blobs: assets, Encryption: encryption}, recovery.HistoryReplayRequest{TargetSHA256: target, ScratchDirectory: scratch})
	if err != nil {
		return report, err
	}
	after, err := configuredRecoveryTarget(ctx, cfg, db, blobs, request.ValkeyIncarnation)
	if err != nil || target != after {
		return report, errors.Join(recovery.ErrConflict, err)
	}
	return completed.Report(), nil
}
