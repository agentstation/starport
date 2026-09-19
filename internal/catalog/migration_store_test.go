package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestMigrationStoreBinding(t *testing.T) {
	for _, withAccepted := range []bool{false, true} {
		name := "embedded-only"
		if withAccepted {
			name = "accepted"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			settings := identityTestSettings(filepath.Join(root, "source"), "", "")
			migration := RuntimeMigration{OperationID: "operation", SourceDirectory: settings.StateDirectory, TargetDirectory: filepath.Join(root, "target"), JournalRoot: filepath.Join(root, "journal"), SourceIdentity: "retained-identity"}
			store := storage.NewMockStore()
			generations, err := NewGenerationStore(store)
			require.NoError(t, err)
			first := runtimeTestGeneration(t, "first", testEmptyCatalog(t, "fixture"), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
			if withAccepted {
				require.NoError(t, generations.Commit(t.Context(), first, ""))
			}
			require.NoError(t, migration.bindStore(t.Context(), store, settings))
			key := migration.storeReceiptKey(settings)
			before, err := store.Get(t.Context(), key)
			require.NoError(t, err)
			require.NoError(t, migration.bindStore(t.Context(), store, settings))
			after, err := store.Get(t.Context(), key)
			require.NoError(t, err)
			require.Equal(t, before, after, "retry must preserve the original binding")
			foreign := storage.NewMockStore()
			require.Error(t, migration.bindStore(t.Context(), foreign, settings), "repeated preparation must not bind another store")
			_, err = foreign.Get(t.Context(), migrationStoreIdentityKey)
			require.ErrorIs(t, err, storage.ErrNotFound, "a refused store must remain unchanged")
			require.NoError(t, store.Delete(t.Context(), key))
			require.NoError(t, migration.bindStore(t.Context(), store, settings), "resume after the journal write and before the KV checkpoint write")
			recovered, err := store.Get(t.Context(), key)
			require.NoError(t, err)
			require.Equal(t, before, recovered)

			changed := migration
			changed.TargetDirectory = filepath.Join(root, "different-target")
			require.Error(t, changed.verifyStore(t.Context(), store, settings))
			require.Error(t, changed.bindStore(t.Context(), store, settings))
			require.Error(t, migration.verifyStore(t.Context(), storage.NewMockStore(), settings))
			next := runtimeTestGeneration(t, "next", testEmptyCatalog(t, "fixture"), first.Manifest.GeneratedAt.Add(time.Hour))
			expected := ""
			if withAccepted {
				expected = first.Manifest.GenerationID
			}
			require.NoError(t, generations.Commit(t.Context(), next, expected))
			require.NoError(t, migration.verifyStore(t.Context(), store, settings), "a valid catalog advance in the bound store may continue")
			if withAccepted {
				older := runtimeTestGeneration(t, "older", testEmptyCatalog(t, "fixture"), first.Manifest.GeneratedAt.Add(-time.Hour))
				require.NoError(t, generations.Commit(t.Context(), older, next.Manifest.GenerationID))
				require.Error(t, migration.verifyStore(t.Context(), store, settings), "a rolled-back catalog must fail closed")
			}
			require.NoError(t, store.Set(t.Context(), key, []byte(`{"version":0}`)))
			require.Error(t, migration.verifyStore(t.Context(), store, settings))
		})
	}
}

func TestMigrationBindingCancellationIsPassive(t *testing.T) {
	root := t.TempDir()
	settings := identityTestSettings(filepath.Join(root, "source"), "", "")
	migration := RuntimeMigration{OperationID: "cancelled", JournalRoot: filepath.Join(root, "journal")}
	store := storage.NewMockStore()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, migration.bindStore(ctx, store, settings), context.Canceled)
	require.NoDirExists(t, migration.JournalRoot)
	_, err := store.Get(t.Context(), migrationStoreIdentityKey)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestMigrationBindingPreservesWriteFailure(t *testing.T) {
	root := t.TempDir()
	settings := identityTestSettings(filepath.Join(root, "source"), "", "")
	migration := RuntimeMigration{OperationID: "write-failure", JournalRoot: filepath.Join(root, "journal")}
	store := storage.NewMockStore()
	require.NoError(t, migration.bindStore(t.Context(), store, settings))
	failure := errors.New("storage write durability is unknown")
	failing := migrationWriteFailureStore{KVStore: store, failure: failure}
	require.ErrorIs(t, migration.bindStore(t.Context(), failing, settings), failure)
}

type migrationWriteFailureStore struct {
	storage.KVStore
	failure error
}

func (s migrationWriteFailureStore) CompareAndSwap(context.Context, string, []byte, []byte) error {
	return s.failure
}
