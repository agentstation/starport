package storage

import (
	"context"
	"errors"
	"time"

	"github.com/dgraph-io/badger/v4"
)

// ReadWithLifetime reads payload and expiry from the same Badger snapshot.
func (s *BadgerStore) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if maxBytes <= 0 {
		return nil, 0, ErrInvalidReadLimit
	}
	if key == "" {
		return nil, 0, ErrInvalidKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, 0, ErrStorageClosed
	}
	var value []byte
	var lifetime time.Duration
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if errors.Is(err, badger.ErrKeyNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if item.IsDeletedOrExpired() {
			return ErrNotFound
		}
		if item.ValueSize() > int64(maxBytes) {
			return ErrValueTooLarge
		}
		if expires := item.ExpiresAt(); expires != 0 {
			expiry := time.Unix(int64(expires), 0) // #nosec G115 -- Badger stores Unix expiry seconds.
			lifetime = time.Until(expiry)
			if lifetime <= 0 {
				return ErrNotFound
			}
		}
		value, err = item.ValueCopy(nil)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return value, lifetime, nil
}
