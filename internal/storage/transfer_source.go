package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// SnapshotSource owns a native connection that exposes only record enumeration.
type SnapshotSource interface {
	RecordSource
	Close() error
}

type snapshotSource struct {
	store    KVStore
	transfer RecordTransfer
}

func (s *snapshotSource) Enumerate(ctx context.Context, yield func(TransferRecord) error) error {
	return s.transfer.Enumerate(ctx, yield)
}
func (s *snapshotSource) Close() error { return s.store.Close() }

// OpenSnapshotSource opens persistent source storage without exposing mutation methods.
// Badger opens read-only where supported. Windows needs an exclusive native open
// that can perform engine recovery; application maintenance stays disabled.
// Shared enumeration binds the observed backend incarnation.
// Observation does not prove external writer fencing.
func OpenSnapshotSource(ctx context.Context, config Config) (SnapshotSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	var store KVStore
	var err error
	switch config.Type {
	case StorageTypeBadger:
		store, err = openBadgerSnapshotSource(config.Badger)
	case StorageTypeValkey:
		store, err = OpenValkey(config.Valkey)
	default:
		return nil, errors.New("backup requires a native persistent record source")
	}
	if err != nil {
		return nil, err
	}
	identity := ""
	if provider, ok := store.(IncarnationProvider); ok {
		identity, err = provider.ObserveIncarnation(ctx)
		if err != nil {
			return nil, errors.Join(err, store.Close())
		}
	}
	if err := CheckImportBarrier(ctx, store); err != nil {
		return nil, errors.Join(err, store.Close())
	}
	transfer, err := OpenRecordTransfer(ctx, store, identity)
	if err != nil {
		return nil, errors.Join(err, store.Close())
	}
	return &snapshotSource{store: store, transfer: transfer}, nil
}

// openBadgerSnapshotSource never creates a missing source. Windows Badger rejects
// read-only opens, so a fenced Windows deployment uses its native exclusive lock.
func openBadgerSnapshotSource(config BadgerConfig) (*BadgerStore, error) {
	if config.InMemory {
		return nil, errors.New("backup requires persistent Badger storage")
	}
	info, err := os.Lstat(config.Path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("backup requires an existing Badger directory")
	}
	manifest, err := os.Lstat(filepath.Join(config.Path, "MANIFEST"))
	if err != nil {
		return nil, err
	}
	if !manifest.Mode().IsRegular() {
		return nil, errors.New("backup requires an existing Badger manifest")
	}
	if runtime.GOOS == "windows" {
		store, err := openBadgerConnection(config, false, false)
		if err != nil {
			return nil, err
		}
		current, err := os.Lstat(config.Path)
		if err != nil {
			return nil, errors.Join(err, store.Close())
		}
		if !os.SameFile(info, current) {
			return nil, errors.Join(errors.New("backup source directory changed"), store.Close())
		}
		return store, nil
	}
	return OpenBadgerReadOnly(config)
}
