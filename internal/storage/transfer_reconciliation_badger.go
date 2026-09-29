package storage

import (
	"context"
	"errors"

	"github.com/dgraph-io/badger/v4"
)

func readBadgerReconciliation(txn *badger.Txn, key string, maximum int) ([]byte, error) {
	item, err := txn.Get([]byte(key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if item.ExpiresAt() != 0 {
		return nil, ErrConflict
	}
	if item.ValueSize() > int64(maximum) {
		return nil, ErrValueTooLarge
	}
	data, err := item.ValueCopy(nil)
	if err == nil && data == nil {
		data = []byte{}
	}
	return data, err
}

func (b *badgerTransfer) ReconcileImport(ctx context.Context, claim []byte, sequence int64, previous, evidence string, mutations []CompareAndSwapMutation) (string, error) {
	receipt, encoded, err := newReconciliationReceipt(claim, sequence, previous, evidence, mutations)
	if err != nil {
		return "", err
	}
	if b.store.config.InMemory || !b.store.config.SyncWrites {
		return "", ErrImportRestricted
	}
	var digest string
	err = b.update(ctx, func(txn *badger.Txn) error {
		plan, err := prepareImportReconciliation(claim, receipt, encoded, mutations, func(key string) ([]byte, error) { return readBadgerReconciliation(txn, key, 4096) })
		if err != nil {
			return err
		}
		if len(plan.writes) > 0 {
			for _, mutation := range mutations {
				current, err := readBadgerReconciliation(txn, mutation.Key, reconciliationMaxBytes)
				if err != nil {
					return err
				}
				if (current == nil) != (mutation.ExpectedValue == nil) || !bytesEqual(current, mutation.ExpectedValue) {
					return ErrConflict
				}
			}
			for _, mutation := range plan.writes {
				if mutation.NewValue == nil {
					err = txn.Delete([]byte(mutation.Key))
				} else {
					err = txn.Set([]byte(mutation.Key), mutation.NewValue)
				}
				if err != nil {
					return err
				}
			}
		}
		digest = plan.digest
		return nil
	})
	if err != nil {
		return "", err
	}
	return digest, nil
}

var _ ImportReconciler = (*badgerTransfer)(nil)
