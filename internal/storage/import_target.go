package storage

import (
	"context"
	"errors"
)

// OpenImportTarget opens native recovery access without clearing import barriers.
// It does not start application maintenance or approve an existing backend incarnation.
// The caller must fence all target writers before opening it.
func OpenImportTarget(ctx context.Context, config Config) (KVStore, RecordTransfer, error) {
	if err := config.Validate(); err != nil {
		return nil, nil, err
	}
	var store KVStore
	var err error
	switch config.Type {
	case StorageTypeBadger:
		if config.Badger.InMemory {
			return nil, nil, errors.New("restore requires persistent KV storage")
		}
		store, err = openBadgerConnection(config.Badger, false, false)
	case StorageTypeValkey:
		store, err = OpenValkey(config.Valkey)
	default:
		return nil, nil, errors.New("restore requires a native KV backend")
	}
	if err != nil {
		return nil, nil, err
	}
	identity := ""
	if provider, ok := store.(IncarnationProvider); ok {
		identity, err = provider.ObserveIncarnation(ctx)
	}
	if err != nil {
		return nil, nil, errors.Join(err, store.Close())
	}
	transfer, err := OpenRecordTransfer(ctx, store, identity)
	if err != nil {
		return nil, nil, errors.Join(err, store.Close())
	}
	return store, transfer, nil
}
