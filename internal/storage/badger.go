// Package storage provides a key-value storage abstraction layer with support
// for multiple backend implementations including embedded and distributed stores.
package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/rs/zerolog/log"
)

// Ensure BadgerStore implements the KVStore interface
var _ KVStore = (*BadgerStore)(nil)

// BadgerStore implements the KVStore interface using Badger DB
type BadgerStore struct {
	db            *badger.DB
	config        BadgerConfig
	gcTicker      *time.Ticker
	gcStop        chan struct{}
	compactTicker *time.Ticker
	compactStop   chan struct{}
	wg            sync.WaitGroup
	closed        bool
	mu            sync.RWMutex
}

// OpenBadger creates a new BadgerStore instance with the given configuration
func OpenBadger(config BadgerConfig) (*BadgerStore, error) {
	return openBadger(config, false)
}

// OpenBadgerReadOnly opens an existing Badger database without background
// maintenance or filesystem creation.
func OpenBadgerReadOnly(config BadgerConfig) (*BadgerStore, error) {
	return openBadger(config, true)
}

func openBadger(config BadgerConfig, readOnly bool) (*BadgerStore, error) {
	if readOnly && config.InMemory {
		return nil, errors.New("in-memory badger cannot open read-only")
	}
	// Ensure the directory exists
	if !config.InMemory {
		if readOnly {
			if err := badgerReadOnlyPreflight(config.Path, runtime.GOOS); err != nil {
				return nil, err
			}
		} else if err := os.MkdirAll(config.Path, 0750); err != nil {
			return nil, fmt.Errorf("failed to create badger directory: %w", err)
		}
	}

	// Configure Badger options for performance
	opts := badger.DefaultOptions(config.Path)
	if config.InMemory {
		opts = badger.DefaultOptions("").WithInMemory(true)
	}
	opts.SyncWrites = config.SyncWrites
	opts.NumVersionsToKeep = config.NumVersions
	opts.NumLevelZeroTables = config.NumLevelZero
	opts.MemTableSize = config.MemTableSize

	// Performance optimizations
	opts.NumMemtables = 5
	opts.NumLevelZeroTablesStall = 10
	opts.NumCompactors = 4
	opts.BlockCacheSize = 256 << 20 // 256 MB
	opts.ReadOnly = readOnly

	// Open the database
	db, err := badger.Open(opts)
	if err != nil {
		return nil, badgerOpenError(readOnly, err)
	}

	store := &BadgerStore{
		db:          db,
		config:      config,
		gcStop:      make(chan struct{}),
		compactStop: make(chan struct{}),
	}

	if !readOnly && !config.InMemory {
		// Start garbage collection for expired keys.
		store.startGarbageCollection()

		// Start periodic compaction.
		store.startCompaction()
	}

	return store, nil
}

func badgerReadOnlyPreflight(path, goos string) error {
	switch goos {
	case "windows":
		return fmt.Errorf("%w: %w", ErrReadOnlyUnsupported, badger.ErrWindowsNotSupported)
	case "plan9":
		return fmt.Errorf("%w: %w", ErrReadOnlyUnsupported, badger.ErrPlan9NotSupported)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read badger directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("badger path is not a directory")
	}
	return nil
}

func badgerOpenError(readOnly bool, err error) error {
	if readOnly && errors.Is(err, badger.ErrTruncateNeeded) {
		return fmt.Errorf("%w: %w", ErrReadOnlyRecoveryRequired, err)
	}
	if readOnly && (errors.Is(err, badger.ErrWindowsNotSupported) || errors.Is(err, badger.ErrPlan9NotSupported)) {
		return fmt.Errorf("%w: %w", ErrReadOnlyUnsupported, err)
	}
	return fmt.Errorf("failed to open badger: %w", err)
}

// Basic operations

// Get retrieves a value by key
func (s *BadgerStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	s.mu.RUnlock()

	if key == "" {
		return nil, ErrInvalidKey
	}

	var value []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrNotFound
			}
			return err
		}

		// Check if the key has expired
		if item.IsDeletedOrExpired() {
			return ErrNotFound
		}

		value, err = item.ValueCopy(nil)
		return err
	})

	if err != nil {
		return nil, err
	}

	return value, nil
}

// Set stores a key-value pair
func (s *BadgerStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	if key == "" {
		return ErrInvalidKey
	}

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), value)
	})
}

// Delete removes a key
func (s *BadgerStore) Delete(_ context.Context, key string) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	if key == "" {
		return ErrInvalidKey
	}

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(key))
	})
}

// Exists checks if a key exists
func (s *BadgerStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return false, ErrStorageClosed
	}
	s.mu.RUnlock()

	if key == "" {
		return false, ErrInvalidKey
	}

	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrNotFound
			}
			return err
		}

		// Check if the key has expired
		if item.IsDeletedOrExpired() {
			return ErrNotFound
		}

		return nil
	})

	if errors.Is(err, ErrNotFound) {
		return false, nil
	}

	return err == nil, err
}

// TTL operations

// SetWithTTL stores a key-value pair with a TTL
// Note: Badger v4 has per-second TTL granularity. TTLs less than 1 second may expire immediately.
func (s *BadgerStore) SetWithTTL(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	if key == "" {
		return ErrInvalidKey
	}

	return s.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte(key), value).WithTTL(ttl)
		return txn.SetEntry(e)
	})
}

// GetTTL returns the TTL for a key
func (s *BadgerStore) GetTTL(_ context.Context, key string) (time.Duration, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return 0, ErrStorageClosed
	}
	s.mu.RUnlock()

	if key == "" {
		return 0, ErrInvalidKey
	}

	var ttl time.Duration
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrNotFound
			}
			return err
		}

		expiresAt := item.ExpiresAt()
		if expiresAt == 0 {
			ttl = 0 // No expiration
		} else {
			expTime := time.Unix(int64(expiresAt), 0) // #nosec G115 - expiresAt is always positive
			ttl = time.Until(expTime)
			if ttl < 0 {
				return ErrNotFound // Key has expired
			}
		}

		return nil
	})

	return ttl, err
}

// ExpireAt sets a key to expire at a specific time
func (s *BadgerStore) ExpireAt(ctx context.Context, key string, expireAt time.Time) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	if key == "" {
		return ErrInvalidKey
	}

	// Get the current value
	value, err := s.Get(ctx, key)
	if err != nil {
		return err
	}

	// Calculate TTL
	ttl := time.Until(expireAt)
	if ttl <= 0 {
		// If expiration time has passed, delete the key
		return s.Delete(ctx, key)
	}

	// Set with new TTL
	return s.SetWithTTL(ctx, key, value, ttl)
}

// Atomic operations

// Increment atomically increments a counter
func (s *BadgerStore) Increment(_ context.Context, key string, delta int64) (int64, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return 0, ErrStorageClosed
	}
	s.mu.RUnlock()

	if key == "" {
		return 0, ErrInvalidKey
	}

	const maxRetries = 100 // Increased for high contention scenarios and race detection
	var newValue int64

	for i := 0; i < maxRetries; i++ {
		err := s.db.Update(func(txn *badger.Txn) error {
			// Get current value
			item, err := txn.Get([]byte(key))
			if err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}

			var currentValue int64
			var ttl time.Duration

			if err == nil {
				// Key exists, get current value
				val, err := item.ValueCopy(nil)
				if err != nil {
					return err
				}
				currentValue, err = DeserializeInt64(val)
				if err != nil {
					return fmt.Errorf("invalid counter value: %w", err)
				}

				// Preserve TTL if set
				expiresAt := item.ExpiresAt()
				if expiresAt > 0 {
					ttl = time.Until(time.Unix(int64(expiresAt), 0)) // #nosec G115
				}
			}

			// Calculate new value
			newValue = currentValue + delta

			// Serialize and store
			serialized := SerializeInt64(newValue)

			if ttl > 0 {
				e := badger.NewEntry([]byte(key), serialized).WithTTL(ttl)
				return txn.SetEntry(e)
			}
			return txn.Set([]byte(key), serialized)
		})

		if err == nil {
			return newValue, nil
		}

		// Check if it's a conflict error
		if errors.Is(err, badger.ErrConflict) {
			// Retry with better backoff strategy
			backoff := time.Duration(1<<uint(i%10)) * time.Microsecond // #nosec G115
			if backoff > 10*time.Millisecond {
				backoff = 10 * time.Millisecond
			}
			time.Sleep(backoff)
			continue
		}

		// For other errors, return immediately
		return 0, err
	}

	// Max retries exceeded
	return 0, fmt.Errorf("increment failed after %d retries: %w", maxRetries, badger.ErrConflict)
}

// Decrement atomically decrements a counter
func (s *BadgerStore) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	return s.Increment(ctx, key, -delta)
}

// CompareAndSwap atomically updates a value if it matches the expected value
func (s *BadgerStore) CompareAndSwap(ctx context.Context, key string, old, newValue []byte) error {
	return s.CompareAndSwapBatch(ctx, []CompareAndSwapMutation{{
		Key: key, ExpectedValue: old, NewValue: newValue,
	}})
}

// CompareAndSwapBatch applies all conditional writes or none of them.
func (s *BadgerStore) CompareAndSwapBatch(_ context.Context, mutations []CompareAndSwapMutation) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	if err := validateCompareAndSwapMutations(mutations); err != nil {
		return err
	}

	err := s.db.Update(func(txn *badger.Txn) error {
		for _, mutation := range mutations {
			item, err := txn.Get([]byte(mutation.Key))
			if err != nil {
				if errors.Is(err, badger.ErrKeyNotFound) && mutation.ExpectedValue == nil {
					if mutation.NewValue == nil {
						continue
					}
					if mutation.TTL > 0 {
						entry := badger.NewEntry([]byte(mutation.Key), mutation.NewValue).WithTTL(mutation.TTL)
						if err := txn.SetEntry(entry); err != nil {
							return err
						}
						continue
					}
					if err := txn.Set([]byte(mutation.Key), mutation.NewValue); err != nil {
						return err
					}
					continue
				}
				if errors.Is(err, badger.ErrKeyNotFound) {
					return ErrConflict
				}
				return err
			}

			currentValue, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			if !bytesEqual(currentValue, mutation.ExpectedValue) {
				return ErrConflict
			}
			if mutation.NewValue == nil {
				if err := txn.Delete([]byte(mutation.Key)); err != nil {
					return err
				}
				continue
			}

			ttl := mutation.TTL
			if ttl == 0 && item.ExpiresAt() > 0 {
				ttl = time.Until(time.Unix(int64(item.ExpiresAt()), 0)) // #nosec G115
			}
			if ttl > 0 {
				entry := badger.NewEntry([]byte(mutation.Key), mutation.NewValue).WithTTL(ttl)
				if err := txn.SetEntry(entry); err != nil {
					return err
				}
				continue
			}
			if err := txn.Set([]byte(mutation.Key), mutation.NewValue); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, badger.ErrConflict) {
		return ErrConflict
	}
	return err
}

// Batch operations

// BatchGet retrieves multiple values by keys
func (s *BadgerStore) BatchGet(_ context.Context, keys []string) (map[string][]byte, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	s.mu.RUnlock()

	result := make(map[string][]byte, len(keys))

	err := s.db.View(func(txn *badger.Txn) error {
		for _, key := range keys {
			if key == "" {
				continue
			}

			item, err := txn.Get([]byte(key))
			if err != nil {
				if errors.Is(err, badger.ErrKeyNotFound) {
					continue
				}
				return err
			}

			// Skip expired keys
			if item.IsDeletedOrExpired() {
				continue
			}

			value, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}

			result[key] = value
		}
		return nil
	})

	return result, err
}

// BatchSet stores multiple key-value pairs
func (s *BadgerStore) BatchSet(_ context.Context, items map[string][]byte) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	return s.db.Update(func(txn *badger.Txn) error {
		for key, value := range items {
			if key == "" {
				continue
			}
			if err := txn.Set([]byte(key), value); err != nil {
				return err
			}
		}
		return nil
	})
}

// BatchDelete removes multiple keys
func (s *BadgerStore) BatchDelete(_ context.Context, keys []string) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	return s.db.Update(func(txn *badger.Txn) error {
		for _, key := range keys {
			if key == "" {
				continue
			}
			if err := txn.Delete([]byte(key)); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}
		}
		return nil
	})
}

// BatchSetWithTTL stores multiple key-value pairs with TTL
func (s *BadgerStore) BatchSetWithTTL(_ context.Context, items map[string][]byte, ttl time.Duration) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	return s.db.Update(func(txn *badger.Txn) error {
		for key, value := range items {
			if key == "" {
				continue
			}
			e := badger.NewEntry([]byte(key), value).WithTTL(ttl)
			if err := txn.SetEntry(e); err != nil {
				return err
			}
		}
		return nil
	})
}

// Transaction support

// BeginTransaction starts a new transaction
func (s *BadgerStore) BeginTransaction(_ context.Context) (Transaction, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	s.mu.RUnlock()

	txn := s.db.NewTransaction(true)
	return &BadgerTransaction{
		txn:    txn,
		store:  s,
		closed: false,
	}, nil
}

// Scan operations

// Scan returns keys matching a pattern
func (s *BadgerStore) Scan(_ context.Context, pattern string, limit int) ([]string, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	s.mu.RUnlock()

	var keys []string
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			if limit > 0 && count >= limit {
				break
			}

			item := it.Item()
			key := string(item.Key())

			// Skip expired keys
			if item.IsDeletedOrExpired() {
				continue
			}

			// Simple pattern matching (prefix for now)
			if pattern == "" || matchesPattern(key, pattern) {
				keys = append(keys, key)
				count++
			}
		}
		return nil
	})

	return keys, err
}

// ScanWithPrefix returns keys with a specific prefix
func (s *BadgerStore) ScanWithPrefix(_ context.Context, prefix string, limit int) ([]string, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	s.mu.RUnlock()

	var keys []string
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefix)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			if limit > 0 && count >= limit {
				break
			}

			item := it.Item()

			// Skip expired keys
			if item.IsDeletedOrExpired() {
				continue
			}

			keys = append(keys, string(item.Key()))
			count++
		}
		return nil
	})

	return keys, err
}

// Health check and lifecycle

// Ping checks if the store is healthy
func (s *BadgerStore) Ping(ctx context.Context) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	// Try a simple operation to verify the database is working
	testKey := "_ping_test_" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := s.Set(ctx, testKey, []byte("ping")); err != nil {
		return err
	}
	return s.Delete(ctx, testKey)
}

// Close closes the store
func (s *BadgerStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// Stop background tasks
	if s.gcTicker != nil {
		s.gcTicker.Stop()
		close(s.gcStop)
	}
	if s.compactTicker != nil {
		s.compactTicker.Stop()
		close(s.compactStop)
	}

	// Wait for background tasks to finish
	s.wg.Wait()

	// Close the database
	return s.db.Close()
}

// Backup and restore utilities

// Backup creates a backup of the database
func (s *BadgerStore) Backup(_ context.Context, path string) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrStorageClosed
	}
	s.mu.RUnlock()

	// Ensure backup directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Create backup file
	f, err := os.Create(path) // #nosec G304 - path is user-provided for backup
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close backup file")
		}
	}()

	// Perform backup
	_, err = s.db.Backup(f, 0)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	return nil
}

// Restore restores the database from a backup
func (s *BadgerStore) Restore(_ context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStorageClosed
	}

	// Open backup file
	f, err := os.Open(path) // #nosec G304 - path is user-provided for restore
	if err != nil {
		return fmt.Errorf("failed to open backup file: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close restore file")
		}
	}()

	// Close current database
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("failed to close current database: %w", err)
	}

	// Remove current database files
	if err := os.RemoveAll(s.config.Path); err != nil {
		return fmt.Errorf("failed to remove current database: %w", err)
	}

	// Create new database directory
	if err := os.MkdirAll(s.config.Path, 0750); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open new database
	opts := badger.DefaultOptions(s.config.Path)
	opts.SyncWrites = s.config.SyncWrites
	db, err := badger.Open(opts)
	if err != nil {
		return fmt.Errorf("failed to open new database: %w", err)
	}

	// Load backup
	if err := db.Load(f, 256); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Error().Err(closeErr).Msg("failed to close database after restore failure")
		}
		return fmt.Errorf("restore failed: %w", err)
	}

	s.db = db
	return nil
}

// Background tasks

// startGarbageCollection starts the garbage collection process for expired keys
func (s *BadgerStore) startGarbageCollection() {
	s.gcTicker = time.NewTicker(5 * time.Minute)
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()

		for {
			select {
			case <-s.gcTicker.C:
				s.runGarbageCollection()
			case <-s.gcStop:
				return
			}
		}
	}()
}

// runGarbageCollection runs the garbage collection process
func (s *BadgerStore) runGarbageCollection() {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	s.mu.RUnlock()

	err := s.db.RunValueLogGC(0.5)
	if err != nil && !errors.Is(err, badger.ErrNoRewrite) {
		log.Error().Err(err).Msg("badger garbage collection failed")
	}
}

// startCompaction starts the periodic compaction process
func (s *BadgerStore) startCompaction() {
	s.compactTicker = time.NewTicker(30 * time.Minute)
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()

		for {
			select {
			case <-s.compactTicker.C:
				s.runCompaction()
			case <-s.compactStop:
				return
			}
		}
	}()
}

// runCompaction runs the compaction process
func (s *BadgerStore) runCompaction() {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	s.mu.RUnlock()

	err := s.db.Flatten(4)
	if err != nil {
		log.Error().Err(err).Msg("badger compaction failed")
	}
}

// Helper functions

// matchesPattern checks if a key matches a pattern (simple glob-style for now)
func matchesPattern(key, pattern string) bool {
	// For now, just support prefix matching with *
	if pattern == "*" {
		return true
	}
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(key) >= len(prefix) && key[:len(prefix)] == prefix
	}
	return key == pattern
}
