package storage

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/dgraph-io/badger/v4"
)

type badgerTransfer struct{ store *BadgerStore }

func (*badgerTransfer) ExpiryResolution() time.Duration { return time.Second }

func (b *badgerTransfer) Enumerate(ctx context.Context, yield func(TransferRecord) error) error {
	if yield == nil {
		return ErrInvalidMutation
	}
	b.store.mu.RLock()
	defer b.store.mu.RUnlock()
	if b.store.closed {
		return ErrStorageClosed
	}
	return b.store.db.View(func(txn *badger.Txn) error {
		if _, err := txn.Get([]byte(TransferBarrierKey)); err == nil {
			return ErrImportRestricted
		} else if !errors.Is(err, badger.ErrKeyNotFound) {
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
			if item.IsDeletedOrExpired() || bytes.Equal(item.Key(), []byte(transferActivationCurrent)) {
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
}

func (b *badgerTransfer) Claim(ctx context.Context, claim []byte) error {
	if b.store.config.InMemory || !b.store.config.SyncWrites {
		return errors.New("KV import requires persistent Badger with sync_writes enabled")
	}
	if err := validateTransferClaim(claim); err != nil {
		return err
	}
	return b.update(ctx, func(txn *badger.Txn) error {
		if err := inactiveBadgerImport(txn); err != nil {
			return err
		}
		item, err := txn.Get([]byte(TransferBarrierKey))
		if err == nil {
			return matchingBadgerImport(item, claim)
		}
		if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()
		it.Rewind()
		if it.Valid() {
			return ErrDatabaseNotEmpty
		}
		return txn.Set([]byte(TransferBarrierKey), claim)
	})
}

func matchingBadgerImport(item *badger.Item, claim []byte) error {
	if item.ExpiresAt() != 0 || item.ValueSize() != int64(len(claim)) {
		return ErrConflict
	}
	return item.Value(func(value []byte) error {
		if !bytes.Equal(value, claim) {
			return ErrConflict
		}
		return nil
	})
}

func (b *badgerTransfer) Import(ctx context.Context, claim []byte, record TransferRecord) error {
	if err := validateTransferClaim(claim); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	expiry := uint64(record.ExpiresAtMillis / 1000) // #nosec G115 -- Validate rejects negative expiry.
	return b.update(ctx, func(txn *badger.Txn) error {
		if err := inactiveBadgerImport(txn); err != nil {
			return err
		}
		barrier, err := txn.Get([]byte(TransferBarrierKey))
		if err != nil {
			return errors.Join(ErrImportRestricted, err)
		}
		if err := matchingBadgerImport(barrier, claim); err != nil {
			return err
		}
		item, err := txn.Get([]byte(record.Key))
		if err == nil {
			if item.ExpiresAt() != expiry || item.ValueSize() != int64(len(record.Value)) {
				return ErrConflict
			}
			return item.Value(func(value []byte) error {
				if !bytes.Equal(value, record.Value) {
					return ErrConflict
				}
				return nil
			})
		}
		if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
		// A positive expiry below one second represents an expired record.
		if record.ExpiresAtMillis != 0 && int64(expiry) <= time.Now().Unix() {
			return nil
		} // #nosec G115 -- Validate bounds expiry.
		entry := badger.NewEntry([]byte(record.Key), record.Value)
		entry.ExpiresAt = expiry
		return txn.SetEntry(entry)
	})
}

func (b *badgerTransfer) update(ctx context.Context, write func(*badger.Txn) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.store.mu.RLock()
	defer b.store.mu.RUnlock()
	if b.store.closed {
		return ErrStorageClosed
	}
	err := b.store.db.Update(func(txn *badger.Txn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return write(txn)
	})
	if errors.Is(err, badger.ErrConflict) {
		return ErrConflict
	}
	return err
}

// inactiveBadgerImport prevents completed operations from reclaiming a barrier.
func inactiveBadgerImport(txn *badger.Txn) error {
	_, err := txn.Get([]byte(transferActivationCurrent))
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, badger.ErrKeyNotFound) {
		return err
	}
	return nil
}
