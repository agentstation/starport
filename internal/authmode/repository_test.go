package authmode

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
)

// TestRepositoryContract runs against every storage backend, because the one
// promise this repository makes is that a console change outlives the process
// that accepted it, and that promise is only as good as the backend under it.
func TestRepositoryContract(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		_, err = repository.Get(ctx)
		require.ErrorIs(t, err, ErrNotFound, "a gateway that was never switched has no stored mode")

		stored, err := repository.Put(ctx, Setting{
			Mode:      Disabled,
			Source:    SourceConsole,
			UpdatedAt: time.Unix(100, 0).UTC(),
		}, 0)
		require.NoError(t, err)
		require.EqualValues(t, 1, stored.Revision)

		read, err := repository.Get(ctx)
		require.NoError(t, err)
		require.Equal(t, stored, read)

		// One deployment-wide switch is one record, not a keyed collection.
		keys, err := store.ScanWithPrefix(ctx, StoragePrefix, 0)
		require.NoError(t, err)
		require.Equal(t, []string{StorageKey}, keys)

		updated, err := repository.Put(ctx, Setting{Mode: Required, Source: SourceConsole}, read.Revision)
		require.NoError(t, err)
		require.EqualValues(t, 2, updated.Revision)

		// Two operators flipping the switch at once must not silently overwrite
		// each other: the loser is told, rather than believing it won.
		_, err = repository.Put(ctx, Setting{Mode: Disabled, Source: SourceConsole}, read.Revision)
		require.ErrorIs(t, err, ErrConflict)

		_, err = repository.Put(ctx, Setting{Mode: "sometimes", Source: SourceConsole}, updated.Revision)
		require.ErrorIs(t, err, ErrInvalidMode)
	})
}

// TestCreateRefusesAnExistingRecord covers the first-write path specifically.
// A zero expected revision means "there is nothing stored", and honoring it
// against an existing record would let a stale caller reopen a gateway an
// operator had just locked.
func TestCreateRefusesAnExistingRecord(t *testing.T) {
	ctx := context.Background()
	repository, err := Open(storage.NewMockStore())
	require.NoError(t, err)

	_, err = repository.Put(ctx, Setting{Mode: Required, Source: SourceConsole}, 0)
	require.NoError(t, err)

	_, err = repository.Put(ctx, Setting{Mode: Disabled, Source: SourceConsole}, 0)
	require.ErrorIs(t, err, ErrConflict)
}

// TestGetRefusesAnUnrecognizedStoredMode is the fail-loud choice. Reading an
// unknown mode as "required" would be safe and silent, and an operator whose
// stored mode stopped applying should be told rather than discover it from a
// 401 they cannot explain.
func TestGetRefusesAnUnrecognizedStoredMode(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	repository, err := Open(store)
	require.NoError(t, err)

	corrupt, err := json.Marshal(map[string]any{
		"schema_version": StorageSchemaVersion,
		"revision":       1,
		"setting":        map[string]any{"mode": "sometimes", "source": "console"},
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(ctx, StorageKey, corrupt))

	_, err = repository.Get(ctx)
	require.ErrorIs(t, err, ErrCorruptRecord)
}

// TestOpenRequiresAStore keeps the storage-less deployment an explicit state
// rather than a repository that accepts writes and forgets them.
func TestOpenRequiresAStore(t *testing.T) {
	_, err := Open(nil)
	require.ErrorIs(t, err, ErrRepositoryRequired)
}
