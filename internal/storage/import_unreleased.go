package storage

import (
	"bytes"
	"context"
	"errors"

	"github.com/dgraph-io/badger/v4"
)

// ImportUnreleasedInspector checks original closed ownership at any replay cursor.
// It grants no replay, repair, or activation authority.
type ImportUnreleasedInspector interface {
	CheckUnreleasedImport(context.Context, []byte) error
}

func unreleasedImportGuards(claim []byte, read func(string) ([]byte, error)) ([]CompareAndSwapMutation, error) {
	if err := validateTransferClaim(claim); err != nil {
		return nil, err
	}
	var guards []CompareAndSwapMutation
	for _, key := range []string{TransferBarrierKey, transferActivationCurrent, transferActivationPrefix + reconciliationDigest(claim)} {
		actual, err := read(key)
		if err != nil {
			return nil, err
		}
		expected := []byte(nil)
		if key == TransferBarrierKey {
			expected = claim
		}
		if !bytes.Equal(actual, expected) || (actual == nil) != (expected == nil) {
			return nil, ErrImportRestricted
		}
		guards = append(guards, CompareAndSwapMutation{Key: key, ExpectedValue: bytes.Clone(actual)})
	}
	return guards, nil
}
func (b *badgerTransfer) CheckUnreleasedImport(ctx context.Context, claim []byte) error {
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
		_, err := unreleasedImportGuards(claim, func(key string) ([]byte, error) { return readBadgerReconciliation(txn, key, 4096) })
		return errors.Join(err, ctx.Err())
	})
}
func (v *valkeyTransfer) CheckUnreleasedImport(ctx context.Context, claim []byte) error {
	if ctx == nil {
		return ErrInvalidMutation
	}
	guards, err := unreleasedImportGuards(claim, func(key string) ([]byte, error) {
		value, ttl, err := v.bound.ReadWithLifetime(ctx, key, 4096)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if ttl != 0 {
			return nil, ErrConflict
		}
		return value, nil
	})
	if err != nil {
		return err
	}
	keys, args := v.importInspectionArguments(guards)
	_, err = v.bound.store.do(ctx, v.bound.store.client.B().Eval().Script(valkeyImportInspectionGuard+`return 1`).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()).AsInt64()
	return transferValkeyError(err)
}
