package storage

import (
	"errors"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
)

// badgerEngineOptions supplies the same engine settings at open and restore.
func badgerEngineOptions(config BadgerConfig, readOnly bool) (badger.Options, error) {
	opts := badger.DefaultOptions(config.Path)
	if config.InMemory {
		opts = badger.DefaultOptions("").WithInMemory(true)
	}
	switch config.Compression {
	case "", "snappy":
		opts.Compression = options.Snappy
	case "none":
		opts.Compression = options.None
	case "zstd":
		opts.Compression = options.ZSTD
	default:
		return badger.Options{}, errors.New("unsupported Badger compression")
	}
	if config.GCInterval < 0 || !(config.GCDiscardRatio > 0 && config.GCDiscardRatio < 1) {
		return badger.Options{}, errors.New("invalid Badger garbage collection settings")
	}
	opts.SyncWrites = config.SyncWrites && !config.InMemory
	opts.NumVersionsToKeep = config.NumVersions
	opts.NumLevelZeroTables = config.NumLevelZero
	opts.MemTableSize = config.MemTableSize
	opts.NumMemtables = 5
	opts.NumLevelZeroTablesStall = 10
	opts.NumCompactors = 4
	opts.BlockCacheSize = 256 << 20
	opts.ReadOnly = readOnly
	return opts, nil
}
