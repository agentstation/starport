package identity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repositorytest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestIdentityRepositoryContract(t *testing.T) {
	repositorytest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)
		suffix := uuid.NewString()
		apiKey := APIKey{
			ID:        "key-" + suffix,
			Name:      "contract-key",
			Hash:      "hash-" + suffix,
			Scopes:    []string{"chat:write"},
			Active:    true,
			CreatedAt: time.Unix(100, 0).UTC(),
			Metadata:  map[string]any{"tenant": "tenant-a"},
		}

		created, err := repository.Create(ctx, apiKey)
		require.NoError(t, err)
		require.EqualValues(t, 1, created.Revision)
		require.Equal(t, apiKey.ID, created.APIKey.ID)

		byID, err := repository.GetByID(ctx, apiKey.ID)
		require.NoError(t, err)
		byHash, err := repository.GetByHash(ctx, apiKey.Hash)
		require.NoError(t, err)
		require.Equal(t, byID, byHash)

		keys, err := store.ScanWithPrefix(ctx, StoragePrefix, 0)
		require.NoError(t, err)
		require.Len(t, keys, 2)
		primaryData, err := store.Get(ctx, identityStorageKey(apiKey.ID))
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(primaryData, &schema))
		require.EqualValues(t, StorageSchemaVersion, schema["schema_version"])

		_, err = repository.Create(ctx, apiKey)
		require.ErrorIs(t, err, ErrConflict)
		duplicateHash := apiKey
		duplicateHash.ID = "other-" + suffix
		_, err = repository.Create(ctx, duplicateHash)
		require.ErrorIs(t, err, ErrConflict)
		_, err = repository.GetByID(ctx, duplicateHash.ID)
		require.ErrorIs(t, err, ErrNotFound)

		listed, err := repository.List(ctx, 10)
		require.NoError(t, err)
		require.Len(t, listed, 1)

		apiKey.Active = false
		updated, err := repository.Update(ctx, apiKey, created.Revision)
		require.NoError(t, err)
		require.EqualValues(t, 2, updated.Revision)
		require.False(t, updated.APIKey.Active)
		_, err = repository.Update(ctx, apiKey, created.Revision)
		require.ErrorIs(t, err, ErrConflict)
		changedHash := apiKey
		changedHash.Hash = "changed-" + suffix
		_, err = repository.Update(ctx, changedHash, updated.Revision)
		require.ErrorIs(t, err, ErrHashImmutable)

		require.ErrorIs(t, repository.Delete(ctx, apiKey.ID, created.Revision), ErrConflict)
		require.NoError(t, repository.Delete(ctx, apiKey.ID, updated.Revision))
		_, err = repository.GetByID(ctx, apiKey.ID)
		require.ErrorIs(t, err, ErrNotFound)
		_, err = repository.GetByHash(ctx, apiKey.Hash)
		require.ErrorIs(t, err, ErrNotFound)

		corruptID := "corrupt-" + suffix
		require.NoError(t, store.Set(ctx, identityStorageKey(corruptID), []byte(`{"schema_version":2}`)))
		_, err = repository.GetByID(ctx, corruptID)
		require.True(t, errors.Is(err, ErrCorruptRecord))
	})
}

func TestIdentityDeleteUsesStoredHashIndexBytes(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	repository, err := Open(store)
	require.NoError(t, err)
	apiKey := APIKey{
		ID: "key-delete-exact", Name: "delete-exact", Hash: "hash-delete-exact",
		Scopes: []string{"chat:write"}, Active: true, CreatedAt: time.Unix(100, 0).UTC(),
	}
	created, err := repository.Create(ctx, apiKey)
	require.NoError(t, err)

	// The record is semantically identical, but its bytes differ from json.Marshal.
	indexData := []byte("{\n  \"identity_id\": \"key-delete-exact\",\n  \"schema_version\": 1\n}")
	require.NoError(t, store.Set(ctx, hashStorageKey(apiKey.Hash), indexData))
	require.NoError(t, repository.Delete(ctx, apiKey.ID, created.Revision))
	_, err = repository.GetByID(ctx, apiKey.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repository.GetByHash(ctx, apiKey.Hash)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIdentityDeleteToleratesMissingHashIndex(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	repository, err := Open(store)
	require.NoError(t, err)
	apiKey := APIKey{
		ID: "key-delete-missing", Name: "delete-missing", Hash: "hash-delete-missing",
		Scopes: []string{"chat:write"}, Active: true, CreatedAt: time.Unix(100, 0).UTC(),
	}
	created, err := repository.Create(ctx, apiKey)
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, hashStorageKey(apiKey.Hash)))
	require.NoError(t, repository.Delete(ctx, apiKey.ID, created.Revision))
	_, err = repository.GetByID(ctx, apiKey.ID)
	require.ErrorIs(t, err, ErrNotFound)
}
