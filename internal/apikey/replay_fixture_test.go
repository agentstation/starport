package apikey

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"slices"
	"testing"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type permissionSnapshot map[string]storage.TransferRecord

func (s permissionSnapshot) ReadCaptured(ctx context.Context, key string, bound int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	record, ok := s[key]
	if !ok {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	if len(record.Value) > bound {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	record.Value = bytes.Clone(record.Value)
	return record, nil
}
func (s permissionSnapshot) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	keys := make([]string, 0, len(s))
	for key := range s {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		record, err := s.ReadCaptured(ctx, key, storage.TransferMaxValueBytes)
		if err != nil {
			return err
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return nil
}
func permissionStore(t *testing.T, backend string) storage.KVStore {
	t.Helper()
	var store storage.KVStore
	var err error
	if backend == "badger" {
		store, err = storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
	} else {
		address := os.Getenv("TEST_VALKEY_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
		}
		store, err = storage.OpenValkey(storage.ValkeyConfig{URL: address, DeploymentID: "permission-replay-" + rand.Text(), AllowInsecure: true})
	}
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}
func permissionTransfer(t *testing.T, store storage.KVStore) storage.RecordTransfer {
	t.Helper()
	identity := ""
	if provider, ok := store.(storage.IncarnationProvider); ok {
		var err error
		identity, err = provider.ObserveIncarnation(t.Context())
		require.NoError(t, err)
	}
	transfer, err := storage.OpenRecordTransfer(t.Context(), store, identity)
	require.NoError(t, err)
	return transfer
}
func capturePermissions(t *testing.T, store storage.KVStore) permissionSnapshot {
	t.Helper()
	result := permissionSnapshot{}
	require.NoError(t, permissionTransfer(t, store).Enumerate(t.Context(), func(record storage.TransferRecord) error {
		record.Value = bytes.Clone(record.Value)
		result[record.Key] = record
		return nil
	}))
	return result
}
func importedPermissions(t *testing.T, backend string, source permissionSnapshot) (storage.KVStore, storage.ImportReconciler, []byte) {
	t.Helper()
	target := permissionStore(t, backend)
	transfer := permissionTransfer(t, target)
	claim := []byte("permission replay")
	require.NoError(t, transfer.Claim(t.Context(), claim))
	for _, record := range source {
		require.NoError(t, transfer.Import(t.Context(), claim, record))
	}
	return target, transfer.(storage.ImportReconciler), claim
}
func advancePermissions(source permissionSnapshot, changes []storage.CompareAndSwapMutation) permissionSnapshot {
	next := permissionSnapshot{}
	for key, record := range source {
		next[key] = record
	}
	for _, change := range changes {
		if change.NewValue == nil {
			delete(next, change.Key)
		} else {
			next[change.Key] = storage.TransferRecord{Key: change.Key, Value: bytes.Clone(change.NewValue)}
		}
	}
	return next
}
