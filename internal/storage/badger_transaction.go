package storage

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
)

// Ensure BadgerTransaction implements the Transaction interface
var _ Transaction = (*BadgerTransaction)(nil)

// BadgerTransaction represents a Badger transaction
type BadgerTransaction struct {
	txn    *badger.Txn
	store  *BadgerStore
	closed bool
	mu     sync.Mutex
}

// Get retrieves a value within the transaction
func (t *BadgerTransaction) Get(key string) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, ErrTransactionClosed
	}

	if key == "" {
		return nil, ErrInvalidKey
	}

	item, err := t.txn.Get([]byte(key))
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Check if the key has expired
	if item.IsDeletedOrExpired() {
		return nil, ErrNotFound
	}

	return item.ValueCopy(nil)
}

// Set stores a key-value pair within the transaction
func (t *BadgerTransaction) Set(key string, value []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrTransactionClosed
	}

	if key == "" {
		return ErrInvalidKey
	}

	return t.txn.Set([]byte(key), value)
}

// Delete removes a key within the transaction
func (t *BadgerTransaction) Delete(key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrTransactionClosed
	}

	if key == "" {
		return ErrInvalidKey
	}

	return t.txn.Delete([]byte(key))
}

// SetWithTTL stores a key-value pair with TTL within the transaction
func (t *BadgerTransaction) SetWithTTL(key string, value []byte, ttl time.Duration) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrTransactionClosed
	}

	if key == "" {
		return ErrInvalidKey
	}

	e := badger.NewEntry([]byte(key), value).WithTTL(ttl)
	return t.txn.SetEntry(e)
}

// Increment atomically increments a counter within the transaction
func (t *BadgerTransaction) Increment(key string, delta int64) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return 0, ErrTransactionClosed
	}

	if key == "" {
		return 0, ErrInvalidKey
	}

	// Get current value
	item, err := t.txn.Get([]byte(key))
	var currentValue int64
	var ttl time.Duration

	if err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
		return 0, err
	}

	if err == nil {
		// Key exists, get current value
		val, err := item.ValueCopy(nil)
		if err != nil {
			return 0, err
		}
		currentValue, err = DeserializeInt64(val)
		if err != nil {
			return 0, err
		}

		// Preserve TTL if set
		expiresAt := item.ExpiresAt()
		if expiresAt > 0 {
			ttl = time.Until(time.Unix(int64(expiresAt), 0)) // #nosec G115 // #nosec G115
		}
	}

	// Calculate new value
	newValue := currentValue + delta

	// Serialize and store
	serialized := SerializeInt64(newValue)

	if ttl > 0 {
		e := badger.NewEntry([]byte(key), serialized).WithTTL(ttl)
		return newValue, t.txn.SetEntry(e)
	}
	return newValue, t.txn.Set([]byte(key), serialized)
}

// CompareAndSwap atomically updates a value if it matches the expected value
func (t *BadgerTransaction) CompareAndSwap(key string, old, newValue []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrTransactionClosed
	}

	if key == "" {
		return ErrInvalidKey
	}

	// Get current value
	item, err := t.txn.Get([]byte(key))
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) && old == nil {
			if newValue == nil {
				return nil
			}
			return t.txn.Set([]byte(key), newValue)
		}
		if errors.Is(err, badger.ErrKeyNotFound) {
			return ErrConflict
		}
		return err
	}

	// Get current value
	currentValue, err := item.ValueCopy(nil)
	if err != nil {
		return err
	}

	// Compare values
	if !bytesEqual(currentValue, old) {
		return ErrConflict
	}
	if newValue == nil {
		return t.txn.Delete([]byte(key))
	}

	// Preserve TTL if set
	var ttl time.Duration
	expiresAt := item.ExpiresAt()
	if expiresAt > 0 {
		ttl = time.Until(time.Unix(int64(expiresAt), 0)) // #nosec G115
	}

	// Set new value
	if ttl > 0 {
		e := badger.NewEntry([]byte(key), newValue).WithTTL(ttl)
		return t.txn.SetEntry(e)
	}
	return t.txn.Set([]byte(key), newValue)
}

// Commit commits the transaction
func (t *BadgerTransaction) Commit(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrTransactionClosed
	}

	err := t.txn.Commit()
	t.closed = true
	if errors.Is(err, badger.ErrConflict) {
		return ErrConflict
	}
	return err
}

// Rollback aborts the transaction
func (t *BadgerTransaction) Rollback() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	t.txn.Discard()
	t.closed = true
	return nil
}
