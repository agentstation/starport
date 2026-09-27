package storage

import (
	"fmt"
	"time"
)

// Open creates the configured KVStore.
func Open(config Config) (KVStore, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid storage config: %w", err)
	}

	switch config.Type {
	case StorageTypeBadger:
		store, err := OpenBadger(config.Badger)
		if err != nil {
			return nil, err
		}
		return store, nil
	case StorageTypeValkey:
		return OpenValkey(config.Valkey)
	default:
		return nil, fmt.Errorf("unknown storage type: %s", config.Type)
	}
}

// OpenReadOnly opens configured storage without permitting a logical write.
func OpenReadOnly(config Config) (KVStore, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid storage config: %w", err)
	}

	var (
		store KVStore
		err   error
	)
	switch config.Type {
	case StorageTypeBadger:
		store, err = OpenBadgerReadOnly(config.Badger)
	case StorageTypeValkey:
		store, err = OpenValkey(config.Valkey)
	default:
		return nil, fmt.Errorf("unknown storage type: %s", config.Type)
	}
	if err != nil {
		return nil, err
	}
	readOnly := &readOnlyStore{KVStore: store}
	if provider, ok := store.(IncarnationProvider); ok {
		return &readOnlyIncarnationProvider{readOnlyStore: readOnly, provider: provider}, nil
	}
	return readOnly, nil
}

// NewMockStore creates a new mock KVStore for testing
func NewMockStore() *MockStore {
	return &MockStore{
		data:         make(map[string][]byte),
		ttl:          make(map[string]time.Time),
		transactions: make(map[string]*MockTransaction),
	}
}
