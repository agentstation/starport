package storage

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/dgraph-io/badger/v4"
)

func (b *badgerTransfer) ReconcileExpiringImport(ctx context.Context, claim []byte, sequence int64, previous, evidence string, records []TransferRecord) (string, error) {
	records, err := copyExpiringImportRecords(ctx, records)
	if err != nil {
		return "", err
	}
	receipt, encoded, mutations, err := expiringReconciliationReceipt(claim, sequence, previous, evidence, records)
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
			for _, record := range records {
				item, err := txn.Get([]byte(record.Key))
				expiry := record.ExpiresAtMillis / 1000
				if errors.Is(err, badger.ErrKeyNotFound) {
					// Badger import rounds original absolute expiry to whole seconds.
					if expiry > time.Now().Unix() {
						return ErrConflict
					}
					continue
				}
				if err != nil {
					return err
				}
				if int64(item.ExpiresAt()) != expiry || item.ValueSize() != int64(len(record.Value)) { // #nosec G115 -- native import bounds expiry.
					return ErrConflict
				}
				if err := item.Value(func(value []byte) error {
					if !bytes.Equal(value, record.Value) {
						return ErrConflict
					}
					return nil
				}); err != nil {
					return err
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

var _ ImportExpiringRetirer = (*badgerTransfer)(nil)
