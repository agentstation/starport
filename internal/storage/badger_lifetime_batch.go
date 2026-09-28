package storage

import (
	"context"
	"errors"
	"time"

	"github.com/dgraph-io/badger/v4"
)

// ReadBatchWithLifetime reads bounded values from one Badger snapshot.
// A failure returns no partial batch. Missing records retain their result slots.
func (s *BadgerStore) ReadBatchWithLifetime(ctx context.Context, keys []string, maxBytes int) ([]LifetimeValue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateLifetimeBatch(keys, maxBytes); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrStorageClosed
	}
	values := make([]LifetimeValue, len(keys))
	err := s.db.View(func(txn *badger.Txn) error {
		for i, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			item, err := txn.Get([]byte(key))
			if errors.Is(err, badger.ErrKeyNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if item.IsDeletedOrExpired() {
				continue
			}
			if item.ValueSize() > int64(maxBytes) {
				return ErrValueTooLarge
			}
			var lifetime time.Duration
			if expires := item.ExpiresAt(); expires != 0 {
				lifetime = time.Until(time.Unix(int64(expires), 0)) // #nosec G115 -- Badger stores Unix expiry seconds.
				if lifetime <= 0 {
					continue
				}
			}
			value, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			values[i] = LifetimeValue{Value: value, Lifetime: lifetime, Found: true}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return values, nil
}
