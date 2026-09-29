package storage

import (
	"context"

	"github.com/dgraph-io/badger/v4"
)

func (b *badgerTransfer) InspectImport(ctx context.Context, claim []byte, position ImportReplayPosition, yield func(TransferRecord) error) error {
	if ctx == nil || yield == nil {
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
	var guards []CompareAndSwapMutation
	err := b.store.db.View(func(txn *badger.Txn) error {
		var err error
		guards, err = prepareImportInspection(claim, position, func(key string) ([]byte, error) { return readBadgerReconciliation(txn, key, 4096) })
		if err != nil {
			return err
		}
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			item := it.Item()
			if item.IsDeletedOrExpired() || importControlKey(string(item.Key())) {
				continue
			}
			if item.KeySize() > TransferMaxKeyBytes || item.ValueSize() > TransferMaxValueBytes || item.ExpiresAt() > uint64(transferMaxExpiry/1000) {
				return ErrValueTooLarge
			}
			value, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			record := TransferRecord{Key: string(item.Key()), Value: value, ExpiresAtMillis: int64(item.ExpiresAt()) * 1000} // #nosec G115 -- the preceding check bounds expiry before conversion.
			if err := record.Validate(); err != nil {
				return err
			}
			if err := yield(record); err != nil {
				return err
			}
		}
		return ctx.Err()
	})
	if err != nil {
		return err
	}
	// Detect a completed replay or activation during a callback, after the read snapshot.
	return b.store.db.View(func(txn *badger.Txn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return checkImportInspection(guards, func(key string) ([]byte, error) { return readBadgerReconciliation(txn, key, 4096) })
	})
}

var _ ImportInspector = (*badgerTransfer)(nil)
