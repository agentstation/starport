package storage

import (
	"context"

	"github.com/dgraph-io/badger/v4"
)

// ImportPositionInspector verifies one exact closed native cursor without enumerating domain records.
// The check grants no mutation or activation authority.
type ImportPositionInspector interface {
	CheckImportPosition(context.Context, []byte, ImportReplayPosition) error
}

// ImportReconciliationSHA256 derives the owner's exact receipt identity without native effects.
// It applies the same claim, mutation, presence, count, and byte checks as native replay.
func ImportReconciliationSHA256(claim []byte, sequence int64, previous, evidence string, mutations []CompareAndSwapMutation) (string, error) {
	_, encoded, err := newReconciliationReceipt(claim, sequence, previous, evidence, mutations)
	if err != nil {
		return "", err
	}
	return reconciliationDigest(encoded), nil
}

func (b *badgerTransfer) CheckImportPosition(ctx context.Context, claim []byte, position ImportReplayPosition) error {
	if ctx == nil {
		return ErrInvalidMutation
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.store.mu.RLock()
	defer b.store.mu.RUnlock()
	if b.store.closed {
		return ErrStorageClosed
	}
	return b.store.db.View(func(txn *badger.Txn) error {
		_, err := prepareImportInspection(claim, position, func(key string) ([]byte, error) { return readBadgerReconciliation(txn, key, 4096) })
		if err != nil {
			return err
		}
		return ctx.Err()
	})
}

func (v *valkeyTransfer) CheckImportPosition(ctx context.Context, claim []byte, position ImportReplayPosition) error {
	if ctx == nil {
		return ErrInvalidMutation
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	guards, err := v.importInspectionGuards(ctx, claim, position)
	if err != nil {
		return err
	}
	keys, args := v.importInspectionArguments(guards)
	store := v.bound.store
	_, err = store.do(ctx, store.client.B().Eval().Script(valkeyImportInspectionGuard+`return 1`).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()).AsInt64()
	return transferValkeyError(err)
}

var _ ImportPositionInspector = (*badgerTransfer)(nil)
var _ ImportPositionInspector = (*valkeyTransfer)(nil)
