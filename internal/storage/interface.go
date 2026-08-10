// Package storage provides a key-value storage abstraction layer with support
// for multiple backend implementations including embedded and distributed stores.
package storage

import (
	"context"
	"errors"
	"time"
)

// Storage backend type constants
const (
	// StorageTypeBadger represents the Badger embedded storage backend
	StorageTypeBadger = "badger"
	// StorageTypeValkey represents the Valkey distributed storage backend
	StorageTypeValkey = "valkey"
)

// Common errors returned by storage operations
var (
	// ErrNotFound is returned when a key does not exist
	ErrNotFound = errors.New("key not found")
	// ErrConflict is returned when a write conflict occurs (e.g., CAS mismatch)
	ErrConflict = errors.New("write conflict")
	// ErrInvalidKey is returned when an invalid key is provided
	ErrInvalidKey = errors.New("invalid key")
	// ErrInvalidMutation is returned when an atomic mutation set is malformed.
	ErrInvalidMutation = errors.New("invalid compare-and-swap mutation")
	// ErrStorageClosed is returned when operations are attempted on a closed store
	ErrStorageClosed = errors.New("storage closed")
	// ErrReadOnly is returned when a write is attempted through a read-only store.
	ErrReadOnly = errors.New("storage is read-only")
	// ErrReadOnlyRecoveryRequired means that a non-mutating inspection cannot
	// open storage until the normal writable startup performs recovery.
	ErrReadOnlyRecoveryRequired = errors.New("storage recovery is required before read-only inspection")
	// ErrReadOnlyUnsupported means that the storage backend cannot provide a
	// non-mutating inspection on the current platform.
	ErrReadOnlyUnsupported = errors.New("read-only storage inspection is unsupported")
	// ErrTimeout is returned when an operation times out
	ErrTimeout = errors.New("operation timeout")
)

// KVStore defines the interface for key-value storage operations.
// All implementations must be thread-safe and support concurrent access.
type KVStore interface {
	// Basic operations
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)

	// TTL operations
	SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error
	GetTTL(ctx context.Context, key string) (time.Duration, error)
	ExpireAt(ctx context.Context, key string, expireAt time.Time) error

	// Atomic operations
	Increment(ctx context.Context, key string, delta int64) (int64, error)
	Decrement(ctx context.Context, key string, delta int64) (int64, error)
	CompareAndSwap(ctx context.Context, key string, old, newValue []byte) error
	CompareAndSwapBatch(ctx context.Context, mutations []CompareAndSwapMutation) error

	// Batch operations
	BatchGet(ctx context.Context, keys []string) (map[string][]byte, error)
	BatchSet(ctx context.Context, items map[string][]byte) error
	BatchDelete(ctx context.Context, keys []string) error
	BatchSetWithTTL(ctx context.Context, items map[string][]byte, ttl time.Duration) error

	// Transaction support
	BeginTransaction(ctx context.Context) (Transaction, error)

	// Scan operations for listing keys
	Scan(ctx context.Context, pattern string, limit int) ([]string, error)
	ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error)

	// Health check and lifecycle
	Ping(ctx context.Context) error
	Close() error
}

// CompareAndSwapMutation is one conditional write in an atomic mutation set.
// A nil ExpectedValue requires absence. A nil NewValue deletes the key. A
// positive TTL replaces the key expiration; zero preserves an existing TTL.
type CompareAndSwapMutation struct {
	Key           string
	ExpectedValue []byte
	NewValue      []byte
	TTL           time.Duration
}

// Transaction represents an atomic set of operations
type Transaction interface {
	// Basic operations within transaction
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error

	// TTL operations within transaction
	SetWithTTL(key string, value []byte, ttl time.Duration) error

	// Atomic operations within transaction
	Increment(key string, delta int64) (int64, error)
	CompareAndSwap(key string, old, newValue []byte) error

	// Transaction control
	Commit(ctx context.Context) error
	Rollback() error
}

// Config represents storage configuration
type Config struct {
	Type   string       `env:"TYPE,default=badger"`
	Badger BadgerConfig `env:",prefix=BADGER_"`
	Valkey ValkeyConfig `env:",prefix=VALKEY_"`
}

// BadgerConfig represents Badger-specific configuration
type BadgerConfig struct {
	Path         string `env:"PATH,default=./data/badger"`
	SyncWrites   bool   `env:"SYNC_WRITES,default=false"`
	Compression  bool   `env:"COMPRESSION,default=true"`
	NumVersions  int    `env:"NUM_VERSIONS,default=1"`
	NumLevelZero int    `env:"NUM_LEVEL_ZERO,default=5"`
	MemTableSize int64  `env:"MEM_TABLE_SIZE,default=67108864"` // 64MB
}

// ValkeyConfig represents Valkey/Redis-specific configuration
type ValkeyConfig struct {
	URL          string        `env:"URL,default=redis://localhost:6379"`
	MaxRetries   int           `env:"MAX_RETRIES,default=3"`
	MinIdleConns int           `env:"MIN_IDLE_CONNS,default=10"`
	MaxConnAge   time.Duration `env:"MAX_CONN_AGE,default=0"`
	PoolTimeout  time.Duration `env:"POOL_TIMEOUT,default=4s"`
	ReadTimeout  time.Duration `env:"READ_TIMEOUT,default=3s"`
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT,default=3s"`
	Password     string        `env:"PASSWORD"`
	DB           int           `env:"DB,default=0"`
	ClusterMode  bool          `env:"CLUSTER_MODE,default=false"`
}

// Validate validates the storage configuration
func (c *Config) Validate() error {
	switch c.Type {
	case StorageTypeBadger, StorageTypeValkey:
		// Valid types
	default:
		return errors.New("invalid storage type: must be 'badger' or 'valkey'")
	}

	if c.Type == StorageTypeBadger {
		if c.Badger.Path == "" {
			return errors.New("badger path cannot be empty")
		}
		if c.Badger.NumVersions < 1 {
			return errors.New("badger num_versions must be at least 1")
		}
		if c.Badger.MemTableSize < 1024*1024 { // 1MB minimum
			return errors.New("badger mem_table_size must be at least 1MB")
		}
	}

	if c.Type == StorageTypeValkey {
		if c.Valkey.URL == "" {
			return errors.New("valkey URL cannot be empty")
		}
		if c.Valkey.MaxRetries < 0 {
			return errors.New("valkey max_retries cannot be negative")
		}
		if c.Valkey.DB < 0 {
			return errors.New("valkey DB index cannot be negative")
		}
	}

	return nil
}
