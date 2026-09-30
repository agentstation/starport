package app

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

type recoveryActivationNative struct {
	db           *sqlstore.DB
	witness      *recovery.Witness
	store        storage.KVStore
	kv           recovery.HistoryKVTarget
	kvTransfer   storage.RecordTransfer
	kvActivate   storage.ImportReplayActivator
	kvInspect    storage.ImportActivationInspector
	blobs        blob.RestoreTarget
	blobReplay   recovery.HistoryBlobTarget
	blobActivate blob.ImportReplayActivator
	blobInspect  blob.ImportActivationInspector
}

func openRecoveryActivationNative(ctx context.Context, cfg *config.Config, request RecoveryActivationRequest) (_ *recoveryActivationNative, resultErr error) {
	if err := validateExistingImportedTargets(cfg, request.History.ValkeyIncarnation); err != nil {
		return nil, err
	}
	n := &recoveryActivationNative{}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, n.close())
		}
	}()
	var err error
	n.db, err = openBackupSQL(cfg)
	if err != nil {
		return nil, err
	}
	n.witness, err = recovery.New(n.db)
	if err != nil {
		return nil, err
	}
	var transfer storage.RecordTransfer
	n.store, transfer, err = storage.OpenImportTarget(ctx, cfg.RuntimeStorage())
	if err != nil {
		return nil, err
	}
	if request.History.ValkeyIncarnation != "" {
		transfer, err = storage.OpenRecordTransfer(ctx, n.store, request.History.ValkeyIncarnation)
		if err != nil {
			return nil, err
		}
	}
	n.kvTransfer = transfer
	var ok bool
	n.kv, ok = transfer.(recovery.HistoryKVTarget)
	if !ok {
		return nil, recovery.ErrConflict
	}
	n.kvActivate, ok = transfer.(storage.ImportReplayActivator)
	if !ok {
		return nil, recovery.ErrConflict
	}
	n.kvInspect, ok = transfer.(storage.ImportActivationInspector)
	if !ok {
		return nil, recovery.ErrConflict
	}
	n.blobs, err = restoreBlobTarget(ctx, cfg.Files)
	if err != nil {
		return nil, err
	}
	n.blobReplay, ok = n.blobs.(recovery.HistoryBlobTarget)
	if !ok {
		return nil, recovery.ErrConflict
	}
	n.blobActivate, ok = n.blobs.(blob.ImportReplayActivator)
	if !ok {
		return nil, recovery.ErrConflict
	}
	n.blobInspect, ok = n.blobs.(blob.ImportActivationInspector)
	if !ok {
		return nil, recovery.ErrConflict
	}
	actual, err := configuredRecoveryTarget(ctx, cfg, n.db, n.blobs, request.History.ValkeyIncarnation)
	if err != nil || actual != request.History.ExpectedTargetSHA256 {
		return nil, errors.Join(recovery.ErrConflict, err)
	}
	return n, nil
}
func (n *recoveryActivationNative) close() error {
	if n == nil {
		return nil
	}
	var errs []error
	if n.store != nil {
		errs = append(errs, n.store.Close())
	}
	if n.db != nil {
		errs = append(errs, n.db.Close())
	}
	return errors.Join(errs...)
}

type passiveActivationCatalog struct{ store storage.KVStore }

func (p passiveActivationCatalog) ReadCatalogTopology(ctx context.Context, key string, limit int) ([]byte, error) {
	return p.store.GetBounded(ctx, key, limit)
}
func (passiveActivationCatalog) CompletedCatalogTopology(context.Context, string, int) (bool, error) {
	return false, recovery.ErrConflict
}
func (passiveActivationCatalog) ApplyCatalogTopology(context.Context, string, int, []storage.CompareAndSwapMutation) error {
	return recovery.ErrConflict
}
