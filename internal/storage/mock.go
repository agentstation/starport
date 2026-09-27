package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MockStore is an in-memory implementation of KVStore for testing
type MockStore struct {
	mu     sync.RWMutex
	data   map[string][]byte
	ttl    map[string]time.Time // Expiration time
	closed bool
}

// Get retrieves a value by key
func (m *MockStore) Get(ctx context.Context, key string) ([]byte, error) {
	return m.getBounded(ctx, key, 0)
}

// GetBounded checks the stored size before copying the value.
func (m *MockStore) GetBounded(ctx context.Context, key string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, ErrInvalidReadLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.getBounded(ctx, key, maxBytes)
}

func (m *MockStore) getBounded(ctx context.Context, key string, maxBytes int) ([]byte, error) {
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

	if maxBytes > 0 && len(value) > maxBytes {
		return nil, ErrValueTooLarge
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
