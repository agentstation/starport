package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/stretchr/testify/require"
)

func TestSnapshotSourceOwnsEnumerationOnlyNativeConnection(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "database")
	_, err := productfiles.CreateDirectory(directory)
	require.NoError(t, err)
	config := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: directory, SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20}}
	writable, err := OpenBadger(config.Badger)
	require.NoError(t, err)
	require.NoError(t, writable.Set(t.Context(), "preserved", []byte("bytes")))
	require.NoError(t, writable.Close())
	source, err := OpenSnapshotSource(t.Context(), config)
	require.NoError(t, err)
	defer func() { require.NoError(t, source.Close()) }()
	_, writes := source.(RecordTransfer)
	require.False(t, writes, "source must not expose native import methods")
	var records []TransferRecord
	require.NoError(t, source.Enumerate(t.Context(), func(record TransferRecord) error { records = append(records, record); return nil }))
	require.Equal(t, []TransferRecord{{Key: "preserved", Value: []byte("bytes")}}, records)
}

func TestSnapshotSourceRefusesMissingOrImportedDatabase(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "imported"} {
		t.Run(kind, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "private")
			_, err := productfiles.CreateDirectory(parent)
			require.NoError(t, err)
			config := Config{Type: StorageTypeBadger, Badger: BadgerConfig{Path: filepath.Join(parent, "database"), SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20}}
			if kind == "empty" {
				require.NoError(t, os.Mkdir(config.Badger.Path, 0o700))
			}
			if kind == "imported" {
				writable, err := OpenBadger(config.Badger)
				require.NoError(t, err)
				require.NoError(t, writable.Set(t.Context(), TransferBarrierKey, []byte("pending")))
				require.NoError(t, writable.Close())
			}
			source, err := OpenSnapshotSource(t.Context(), config)
			require.Error(t, err)
			require.Nil(t, source)
			if kind == "empty" {
				contents, err := os.ReadDir(config.Badger.Path)
				require.NoError(t, err)
				require.Empty(t, contents)
			}
			if kind == "missing" {
				require.NoDirExists(t, config.Badger.Path)
			} else if kind == "imported" {
				require.ErrorIs(t, err, ErrImportRestricted)
			}
		})
	}
}

func TestSnapshotSourceExclusiveConnectionOmitsApplicationMaintenance(t *testing.T) {
	config := BadgerConfig{Path: filepath.Join(t.TempDir(), "database"), SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20}
	source, err := openBadgerConnection(config, false, false)
	require.NoError(t, err)
	defer func() { require.NoError(t, source.Close()) }()
	require.Nil(t, source.gcTicker)
	require.Nil(t, source.compactTicker)
	_, err = openBadgerConnection(config, false, false)
	require.Error(t, err, "a second connection must not bypass the native exclusive lock")
}

func TestSnapshotSourceSharedEnumerationAndImportBarrier(t *testing.T) {
	store, _ := transferTestStore(t, StorageTypeValkey)
	selected := Config{Type: StorageTypeValkey, Valkey: store.(*ValkeyStore).config}
	require.NoError(t, store.Set(t.Context(), "preserved", []byte("shared bytes")))
	source, err := OpenSnapshotSource(t.Context(), selected)
	require.NoError(t, err)
	defer func() { require.NoError(t, source.Close()) }()
	var records []TransferRecord
	require.NoError(t, source.Enumerate(t.Context(), func(record TransferRecord) error { records = append(records, record); return nil }))
	require.Equal(t, []TransferRecord{{Key: "preserved", Value: []byte("shared bytes")}}, records)
	require.NoError(t, store.Set(t.Context(), TransferBarrierKey, []byte("pending")))
	require.ErrorIs(t, source.Enumerate(t.Context(), func(TransferRecord) error { t.Fatal("restricted source yielded a record"); return nil }), ErrImportRestricted)
	other, err := OpenSnapshotSource(t.Context(), selected)
	require.ErrorIs(t, err, ErrImportRestricted)
	require.Nil(t, other)
}
