package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/require"
)

func privateBadgerBackupDirectory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private")
	_, err := productfiles.CreateDirectory(path)
	require.NoError(t, err)
	return path
}

func badgerBackupFixture(t *testing.T) (*BadgerStore, string, BadgerBackup, BadgerConfig) {
	t.Helper()
	config := BadgerConfig{InMemory: true, SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20}
	store, err := OpenBadger(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.NoError(t, store.Set(t.Context(), "retained", []byte("committed")))
	backup := filepath.Join(privateBadgerBackupDirectory(t), "backup")
	receipt, err := store.Backup(t.Context(), backup)
	require.NoError(t, err)
	config.InMemory = false
	config.Path = filepath.Join(privateBadgerBackupDirectory(t), "restored")
	return store, backup, receipt, config
}

func TestBadgerRestorePreservesOriginalAndRejectsExistingDestination(t *testing.T) {
	store, backup, receipt, config := badgerBackupFixture(t)
	existing, err := OpenBadger(config)
	require.NoError(t, err)
	require.NoError(t, existing.Set(t.Context(), "old", []byte("keep")))
	result, err := RestoreBadger(t.Context(), config, backup, receipt)
	require.ErrorIs(t, err, os.ErrExist)
	require.False(t, result.Published)
	value, err := existing.Get(t.Context(), "old")
	require.NoError(t, err)
	require.Equal(t, "keep", string(value))
	require.NoError(t, existing.Close())
	// The Badger engine independently checks the persisted original records.
	db, err := badger.Open(badger.DefaultOptions(config.Path).WithLogger(nil))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.View(func(txn *badger.Txn) error { _, err := txn.Get([]byte("old")); return err }))
	value, err = store.Get(t.Context(), "retained")
	require.NoError(t, err)
	require.Equal(t, "committed", string(value))
}

func TestBadgerRestoreRejectsInvalidBackupBeforePublication(t *testing.T) {
	for _, name := range []string{"digest", "size", "short-frame", "oversize-frame", "protobuf", "missing"} {
		t.Run(name, func(t *testing.T) {
			store, backup, receipt, config := badgerBackupFixture(t)
			switch name {
			case "digest":
				receipt.SHA256 = strings.Repeat("0", 64)
			case "size":
				receipt.Size++
			case "missing":
				require.NoError(t, os.Remove(backup))
			default:
				data := []byte{1, 2, 3}
				if name == "oversize-frame" {
					data = make([]byte, 8)
					binary.LittleEndian.PutUint64(data, ^uint64(0))
				}
				if name == "protobuf" {
					data = make([]byte, 9)
					binary.LittleEndian.PutUint64(data, 1)
					data[8] = 0xff
				}
				require.NoError(t, os.WriteFile(backup, data, 0o600))
				receipt.Size = int64(len(data))
				receipt.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
			}
			result, err := RestoreBadger(t.Context(), config, backup, receipt)
			require.Error(t, err)
			require.False(t, result.Published)
			_, err = os.Lstat(config.Path)
			require.ErrorIs(t, err, os.ErrNotExist)
			value, err := store.Get(t.Context(), "retained")
			require.NoError(t, err)
			require.Equal(t, "committed", string(value))
			entries, err := os.ReadDir(filepath.Dir(config.Path))
			require.NoError(t, err)
			require.Empty(t, entries, "failed private candidates must not remain")
		})
	}
}

func TestBadgerRestoreKeepsAbsoluteExpiryAndOptions(t *testing.T) {
	store, _, _, config := badgerBackupFixture(t)
	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	require.NoError(t, store.ExpireAt(t.Context(), "retained", expiry))
	backup := filepath.Join(privateBadgerBackupDirectory(t), "expiry")
	receipt, err := store.Backup(t.Context(), backup)
	require.NoError(t, err)
	result, err := RestoreBadger(t.Context(), config, backup, receipt)
	require.NoError(t, err)
	require.True(t, result.Published)
	restored, err := OpenBadger(config)
	require.NoError(t, err)
	defer restored.Close()
	require.True(t, restored.db.Opts().SyncWrites)
	require.NoError(t, restored.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("retained"))
		if err != nil {
			return err
		}
		require.Equal(t, expiry.Unix(), int64(item.ExpiresAt()))
		return nil
	}))
}

func TestBadgerRestoreConcurrentPublicationHasOneWinner(t *testing.T) {
	_, backup, receipt, config := badgerBackupFixture(t)
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Go(func() {
			result, err := RestoreBadger(t.Context(), config, backup, receipt)
			if err != nil && !errors.Is(err, os.ErrExist) {
				t.Errorf("restore: %v", err)
			}
			results <- result.Published
		})
	}
	wg.Wait()
	close(results)
	published := 0
	for yes := range results {
		if yes {
			published++
		}
	}
	require.Equal(t, 1, published)
	restored, err := OpenBadger(config)
	require.NoError(t, err)
	defer restored.Close()
	value, err := restored.Get(t.Context(), "retained")
	require.NoError(t, err)
	require.Equal(t, "committed", string(value))
}

func TestBadgerBackupNeverReplacesAndHonorsCancellation(t *testing.T) {
	store, backup, receipt, config := badgerBackupFixture(t)
	_, err := store.Backup(t.Context(), backup)
	require.ErrorIs(t, err, os.ErrExist)
	data, err := os.ReadFile(backup)
	require.NoError(t, err)
	require.Equal(t, receipt.SHA256, fmt.Sprintf("%x", sha256.Sum256(data)))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = store.Backup(ctx, filepath.Join(filepath.Dir(backup), "canceled"))
	require.ErrorIs(t, err, context.Canceled)
	result, err := RestoreBadger(ctx, config, backup, receipt)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, result.Published)
}
