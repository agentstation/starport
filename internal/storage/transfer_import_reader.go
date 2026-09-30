package storage

import (
	"context"
	"errors"

	"github.com/dgraph-io/badger/v4"
)

// ImportRecordReader reads one bounded record at an exact closed native replay position.
// It does not enumerate unrelated records or expose current native import controls.
// External writer fencing must cover reads and the later owner transaction.
type ImportRecordReader interface {
	ReadImportAt(context.Context, []byte, ImportReplayPosition, string, int) (TransferRecord, error)
}

func validImportRead(ctx context.Context, key string, limit int) error {
	if ctx == nil || key == "" || len(key) > TransferMaxKeyBytes || importControlKey(key) || limit <= 0 || limit > TransferMaxValueBytes {
		return ErrInvalidMutation
	}
	return ctx.Err()
}

func (b *badgerTransfer) ReadImportAt(ctx context.Context, claim []byte, position ImportReplayPosition, key string, limit int) (TransferRecord, error) {
	if err := validImportRead(ctx, key, limit); err != nil {
		return TransferRecord{}, err
	}
	b.store.mu.RLock()
	defer b.store.mu.RUnlock()
	if b.store.closed {
		return TransferRecord{}, ErrStorageClosed
	}
	var result TransferRecord
	err := b.store.db.View(func(txn *badger.Txn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := prepareImportInspection(claim, position, func(key string) ([]byte, error) { return readBadgerReconciliation(txn, key, 4096) }); err != nil {
			return err
		}
		item, err := txn.Get([]byte(key))
		if errors.Is(err, badger.ErrKeyNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if item.ValueSize() > int64(limit) || item.ExpiresAt() > uint64(transferMaxExpiry/1000) {
			return ErrValueTooLarge
		}
		value, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		result = TransferRecord{Key: key, Value: value, ExpiresAtMillis: int64(item.ExpiresAt()) * 1000} // #nosec G115 -- the preceding check bounds expiry before conversion.
		if err := result.Validate(); err != nil {
			return err
		}
		return ctx.Err()
	})
	if err != nil {
		return TransferRecord{}, err
	}
	return result, nil
}

func (v *valkeyTransfer) ReadImportAt(ctx context.Context, claim []byte, position ImportReplayPosition, key string, limit int) (TransferRecord, error) {
	if err := validImportRead(ctx, key, limit); err != nil {
		return TransferRecord{}, err
	}
	guards, err := v.importInspectionGuards(ctx, claim, position)
	if err != nil {
		return TransferRecord{}, err
	}
	keys, args := v.importInspectionArguments(guards)
	return v.readImportInspection(ctx, key, keys, args, limit)
}

var _ ImportRecordReader = (*badgerTransfer)(nil)
var _ ImportRecordReader = (*valkeyTransfer)(nil)
