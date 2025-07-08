package storage

import (
	"fmt"
	"time"
)

// Open creates a new KVStore instance based on the configuration.
// This follows the Go convention of using Open for creating connections.
func Open(config Config) (KVStore, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid storage config: %w", err)
	}

	switch config.Type {
	case StorageTypeBadger:
		// TODO: Implement in P1-S2-2.2
		// return OpenBadger(config.Badger)
		return nil, fmt.Errorf("badger store not yet implemented")
	case StorageTypeValkey:
		// TODO: Implement when needed
		// return OpenValkey(config.Valkey)
		return nil, fmt.Errorf("valkey store not yet implemented")
	default:
		return nil, fmt.Errorf("unknown storage type: %s", config.Type)
	}
}

// NewMockStore creates a new mock KVStore for testing
func NewMockStore() *MockStore {
	return &MockStore{
		data:         make(map[string][]byte),
		ttl:          make(map[string]time.Time),
		transactions: make(map[string]*MockTransaction),
	}
}