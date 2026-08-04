package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Transaction operation type constants
const (
	opTypeSet        = "set"
	opTypeSetWithTTL = "setWithTTL"
	opTypeDelete     = "delete"
	opTypeIncrement  = "increment"
)

// MockStore is an in-memory implementation of KVStore for testing
type MockStore struct {
	mu           sync.RWMutex
	data         map[string][]byte
	ttl          map[string]time.Time // Expiration time
	closed       bool
	transactions map[string]*MockTransaction
	txCounter    int
}

// Get retrieves a value by key
func (m *MockStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := m.checkContext(ctx); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	if err := m.checkExpired(key); err != nil {
		return nil, err
	}

	value, exists := m.data[key]
	if !exists {
		return nil, ErrNotFound
	}

	// Return a copy to prevent external modification
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}

// Set stores a value by key
func (m *MockStore) Set(ctx context.Context, key string, value []byte) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	if key == "" {
		return ErrInvalidKey
	}

	// Store a copy to prevent external modification
	storedValue := make([]byte, len(value))
	copy(storedValue, value)
	m.data[key] = storedValue
	delete(m.ttl, key) // Remove any existing TTL
	return nil
}

// Delete removes a key
func (m *MockStore) Delete(ctx context.Context, key string) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	delete(m.data, key)
	delete(m.ttl, key)
	return nil
}

// Exists checks if a key exists
func (m *MockStore) Exists(ctx context.Context, key string) (bool, error) {
	if err := m.checkContext(ctx); err != nil {
		return false, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return false, ErrStorageClosed
	}

	if err := m.checkExpired(key); err != nil {
		return false, nil
	}

	_, exists := m.data[key]
	return exists, nil
}

// SetWithTTL stores a value with expiration
func (m *MockStore) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	if key == "" {
		return ErrInvalidKey
	}

	// Store a copy to prevent external modification
	storedValue := make([]byte, len(value))
	copy(storedValue, value)
	m.data[key] = storedValue
	m.ttl[key] = time.Now().Add(ttl)
	return nil
}

// GetTTL returns the remaining TTL for a key
func (m *MockStore) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	if err := m.checkContext(ctx); err != nil {
		return 0, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return 0, ErrStorageClosed
	}

	expireAt, exists := m.ttl[key]
	if !exists {
		if _, hasKey := m.data[key]; !hasKey {
			return 0, ErrNotFound
		}
		return -1, nil // No expiration
	}

	remaining := time.Until(expireAt)
	if remaining < 0 {
		return 0, nil
	}
	return remaining, nil
}

// ExpireAt sets an absolute expiration time for a key
func (m *MockStore) ExpireAt(ctx context.Context, key string, expireAt time.Time) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	if _, exists := m.data[key]; !exists {
		return ErrNotFound
	}

	m.ttl[key] = expireAt
	return nil
}

// Increment atomically increments a counter
func (m *MockStore) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	if err := m.checkContext(ctx); err != nil {
		return 0, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, ErrStorageClosed
	}

	var current int64
	if data, exists := m.data[key]; exists {
		var err error
		current, err = DeserializeInt64(data)
		if err != nil {
			return 0, fmt.Errorf("value is not a valid number: %w", err)
		}
	}

	newValue := current + delta
	m.data[key] = SerializeInt64(newValue)
	return newValue, nil
}

// Decrement atomically decrements a counter
func (m *MockStore) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	return m.Increment(ctx, key, -delta)
}

// CompareAndSwap atomically updates a value if it matches the expected value
func (m *MockStore) CompareAndSwap(ctx context.Context, key string, old, newValue []byte) error {
	return m.CompareAndSwapBatch(ctx, []CompareAndSwapMutation{{
		Key: key, ExpectedValue: old, NewValue: newValue,
	}})
}

// CompareAndSwapBatch applies all conditional writes or none of them.
func (m *MockStore) CompareAndSwapBatch(ctx context.Context, mutations []CompareAndSwapMutation) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}
	if err := validateCompareAndSwapMutations(mutations); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	for _, mutation := range mutations {
		_ = m.checkExpired(mutation.Key)
		current, exists := m.data[mutation.Key]
		if !exists && mutation.ExpectedValue != nil {
			return ErrConflict
		}
		if exists && !bytesEqual(current, mutation.ExpectedValue) {
			return ErrConflict
		}
	}

	for _, mutation := range mutations {
		if mutation.NewValue == nil {
			delete(m.data, mutation.Key)
			delete(m.ttl, mutation.Key)
			continue
		}
		storedValue := append([]byte(nil), mutation.NewValue...)
		m.data[mutation.Key] = storedValue
		if mutation.TTL > 0 {
			m.ttl[mutation.Key] = time.Now().Add(mutation.TTL)
		}
	}
	return nil
}

// BatchGet retrieves multiple values
func (m *MockStore) BatchGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	if err := m.checkContext(ctx); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	result := make(map[string][]byte)
	for _, key := range keys {
		if err := m.checkExpired(key); err != nil {
			continue
		}
		if value, exists := m.data[key]; exists {
			// Return a copy
			copiedValue := make([]byte, len(value))
			copy(copiedValue, value)
			result[key] = copiedValue
		}
	}
	return result, nil
}

// BatchSet stores multiple values
func (m *MockStore) BatchSet(ctx context.Context, items map[string][]byte) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	for key, value := range items {
		if key == "" {
			return ErrInvalidKey
		}
		// Store a copy
		storedValue := make([]byte, len(value))
		copy(storedValue, value)
		m.data[key] = storedValue
		delete(m.ttl, key)
	}
	return nil
}

// BatchDelete removes multiple keys
func (m *MockStore) BatchDelete(ctx context.Context, keys []string) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	for _, key := range keys {
		delete(m.data, key)
		delete(m.ttl, key)
	}
	return nil
}

// BatchSetWithTTL stores multiple values with TTL
func (m *MockStore) BatchSetWithTTL(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	expireAt := time.Now().Add(ttl)
	for key, value := range items {
		if key == "" {
			return ErrInvalidKey
		}
		// Store a copy
		storedValue := make([]byte, len(value))
		copy(storedValue, value)
		m.data[key] = storedValue
		m.ttl[key] = expireAt
	}
	return nil
}

// BeginTransaction starts a new transaction
func (m *MockStore) BeginTransaction(ctx context.Context) (Transaction, error) {
	if err := m.checkContext(ctx); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	m.txCounter++
	txID := fmt.Sprintf("tx_%d", m.txCounter)
	tx := &MockTransaction{
		id:      txID,
		store:   m,
		pending: make(map[string]*txOp),
	}
	m.transactions[txID] = tx
	return tx, nil
}

// Scan returns keys matching a pattern
func (m *MockStore) Scan(ctx context.Context, pattern string, limit int) ([]string, error) {
	if err := m.checkContext(ctx); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	var keys []string
	for key := range m.data {
		if err := m.checkExpired(key); err != nil {
			continue
		}
		if matchPattern(key, pattern) {
			keys = append(keys, key)
			if limit > 0 && len(keys) >= limit {
				break
			}
		}
	}
	return keys, nil
}

// ScanWithPrefix returns keys with a specific prefix
func (m *MockStore) ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error) {
	if err := m.checkContext(ctx); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	var keys []string
	for key := range m.data {
		if err := m.checkExpired(key); err != nil {
			continue
		}
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
			if limit > 0 && len(keys) >= limit {
				break
			}
		}
	}
	return keys, nil
}

// Ping checks if the store is healthy
func (m *MockStore) Ping(ctx context.Context) error {
	if err := m.checkContext(ctx); err != nil {
		return err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return ErrStorageClosed
	}
	return nil
}

// Close closes the store
func (m *MockStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}
	m.closed = true
	return nil
}

// Helper methods

func (m *MockStore) checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ErrTimeout
	default:
		return nil
	}
}

func (m *MockStore) checkExpired(key string) error {
	if expireAt, exists := m.ttl[key]; exists {
		if time.Now().After(expireAt) || time.Now().Equal(expireAt) {
			delete(m.data, key)
			delete(m.ttl, key)
			return ErrNotFound
		}
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func matchPattern(str, pattern string) bool {
	// Simple pattern matching: * matches any sequence of characters
	if pattern == "*" {
		return true
	}
	// This is a simplified implementation
	// In production, use a proper glob matching library
	return strings.Contains(str, strings.ReplaceAll(pattern, "*", ""))
}

// MockTransaction represents a mock transaction
type MockTransaction struct {
	id       string
	store    *MockStore
	pending  map[string]*txOp
	mu       sync.Mutex
	finished bool
}

type txOp struct {
	opType string
	value  []byte
	ttl    time.Duration
	delta  int64
}

// Get retrieves a value within the transaction
func (t *MockTransaction) Get(key string) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finished {
		return nil, errors.New("transaction already finished")
	}

	// Check pending operations first
	if op, exists := t.pending[key]; exists {
		if op.opType == opTypeDelete {
			return nil, ErrNotFound
		}
		if op.opType == opTypeSet || op.opType == opTypeSetWithTTL {
			result := make([]byte, len(op.value))
			copy(result, op.value)
			return result, nil
		}
	}

	// Release lock before calling store to avoid deadlock
	t.mu.Unlock()
	value, err := t.store.Get(context.Background(), key)
	t.mu.Lock()

	// Re-check if transaction was finished while we didn't hold the lock
	if t.finished {
		return nil, errors.New("transaction already finished")
	}

	return value, err
}

// Set stores a value within the transaction
func (t *MockTransaction) Set(key string, value []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finished {
		return errors.New("transaction already finished")
	}

	if key == "" {
		return ErrInvalidKey
	}

	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)
	t.pending[key] = &txOp{opType: opTypeSet, value: valueCopy}
	return nil
}

// Delete removes a key within the transaction
func (t *MockTransaction) Delete(key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finished {
		return errors.New("transaction already finished")
	}

	t.pending[key] = &txOp{opType: opTypeDelete}
	return nil
}

// SetWithTTL stores a value with TTL within the transaction
func (t *MockTransaction) SetWithTTL(key string, value []byte, ttl time.Duration) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finished {
		return errors.New("transaction already finished")
	}

	if key == "" {
		return ErrInvalidKey
	}

	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)
	t.pending[key] = &txOp{opType: opTypeSetWithTTL, value: valueCopy, ttl: ttl}
	return nil
}

// Increment atomically increments within the transaction
func (t *MockTransaction) Increment(key string, delta int64) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finished {
		return 0, errors.New("transaction already finished")
	}

	// Get current value
	var current int64
	if op, exists := t.pending[key]; exists && op.opType == opTypeIncrement {
		current = op.delta
	} else {
		// Check pending operations for set/setWithTTL
		if op, exists := t.pending[key]; exists {
			switch op.opType {
			case opTypeDelete:
				current = 0 // Deleted keys start from 0
			case opTypeSet, opTypeSetWithTTL:
				var err error
				current, err = DeserializeInt64(op.value)
				if err != nil {
					return 0, fmt.Errorf("value is not a valid number: %w", err)
				}
			}
		} else {
			// Need to check store - release lock to avoid deadlock
			t.mu.Unlock()
			data, err := t.store.Get(context.Background(), key)
			t.mu.Lock()

			// Re-check if transaction was finished while we didn't hold the lock
			if t.finished {
				return 0, errors.New("transaction already finished")
			}

			if err != nil && err != ErrNotFound {
				return 0, err
			}
			if data != nil {
				current, err = DeserializeInt64(data)
				if err != nil {
					return 0, fmt.Errorf("value is not a valid number: %w", err)
				}
			}
		}
	}

	newValue := current + delta
	t.pending[key] = &txOp{opType: opTypeIncrement, delta: newValue, value: SerializeInt64(newValue)}
	return newValue, nil
}

// CompareAndSwap performs CAS within the transaction
func (t *MockTransaction) CompareAndSwap(key string, old, newValue []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finished {
		return errors.New("transaction already finished")
	}

	// Get current value - check pending operations first
	var current []byte
	var err error

	if op, exists := t.pending[key]; exists {
		switch op.opType {
		case opTypeDelete:
			err = ErrNotFound
		case opTypeSet, opTypeSetWithTTL:
			current = make([]byte, len(op.value))
			copy(current, op.value)
		}
	} else {
		// Need to check store - release lock to avoid deadlock
		t.mu.Unlock()
		current, err = t.store.Get(context.Background(), key)
		t.mu.Lock()

		// Re-check if transaction was finished while we didn't hold the lock
		if t.finished {
			return errors.New("transaction already finished")
		}
	}

	if err != nil && err != ErrNotFound {
		return err
	}

	if err == ErrNotFound && old != nil {
		return ErrConflict
	}
	if err == nil && !bytesEqual(current, old) {
		return ErrConflict
	}

	if newValue == nil {
		t.pending[key] = &txOp{opType: opTypeDelete}
	} else {
		valueCopy := make([]byte, len(newValue))
		copy(valueCopy, newValue)
		t.pending[key] = &txOp{opType: opTypeSet, value: valueCopy}
	}
	return nil
}

// Commit applies all pending operations
func (t *MockTransaction) Commit(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finished {
		return errors.New("transaction already finished")
	}
	t.finished = true

	// Apply all operations
	t.store.mu.Lock()
	defer t.store.mu.Unlock()

	for key, op := range t.pending {
		switch op.opType {
		case opTypeSet:
			t.store.data[key] = op.value
			delete(t.store.ttl, key)
		case opTypeSetWithTTL:
			t.store.data[key] = op.value
			t.store.ttl[key] = time.Now().Add(op.ttl)
		case opTypeDelete:
			delete(t.store.data, key)
			delete(t.store.ttl, key)
		case opTypeIncrement:
			t.store.data[key] = op.value
		}
	}

	delete(t.store.transactions, t.id)
	return nil
}

// Rollback discards all pending operations
func (t *MockTransaction) Rollback() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.finished {
		return errors.New("transaction already finished")
	}
	t.finished = true

	t.store.mu.Lock()
	delete(t.store.transactions, t.id)
	t.store.mu.Unlock()

	return nil
}
