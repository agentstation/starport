package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestProviderCredentialRepositoryContract(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)
		suffix := uuid.NewString()
		key := ProviderKey{
			Scope:               "user:" + suffix,
			Provider:            "provider/example-" + suffix,
			EncryptedCredential: "encrypted-value",
			Config:              map[string]any{"region": "test"},
			Priority:            1,
			CreatedAt:           time.Unix(100, 0).UTC(),
			UpdatedAt:           time.Unix(100, 0).UTC(),
		}

		created, err := repository.Create(ctx, key)
		require.NoError(t, err)
		require.EqualValues(t, 1, created.Revision)
		stored, err := repository.Get(ctx, key.Scope, key.Provider)
		require.NoError(t, err)
		require.Equal(t, created, stored)

		storageKeys, err := store.ScanWithPrefix(ctx, ProviderCredentialStoragePrefix, 0)
		require.NoError(t, err)
		require.Equal(t, []string{StorageKey(key.Scope, key.Provider)}, storageKeys)
		data, err := store.Get(ctx, storageKeys[0])
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(data, &schema))
		require.EqualValues(t, ProviderCredentialStorageSchemaVersion, schema["schema_version"])

		_, err = repository.Create(ctx, key)
		require.ErrorIs(t, err, ErrConflict)
		scopeRecords, err := repository.ListScope(ctx, key.Scope, 10)
		require.NoError(t, err)
		require.Len(t, scopeRecords, 1)
		allRecords, err := repository.ListAll(ctx, 10)
		require.NoError(t, err)
		require.Len(t, allRecords, 1)

		key.UsageCount = 7
		updated, err := repository.Update(ctx, key, created.Revision)
		require.NoError(t, err)
		require.EqualValues(t, 2, updated.Revision)
		_, err = repository.Update(ctx, key, created.Revision)
		require.ErrorIs(t, err, ErrConflict)

		invalid := key
		invalid.Scope = ""
		_, err = repository.Create(ctx, invalid)
		require.ErrorIs(t, err, ErrInvalidScope)

		require.ErrorIs(t, repository.Delete(ctx, key.Scope, key.Provider, created.Revision), ErrConflict)
		require.NoError(t, repository.Delete(ctx, key.Scope, key.Provider, updated.Revision))
		_, err = repository.Get(ctx, key.Scope, key.Provider)
		require.ErrorIs(t, err, ErrNotFound)

		corruptProvider := "corrupt-" + suffix
		require.NoError(t, store.Set(ctx, StorageKey(key.Scope, corruptProvider), []byte(`{"schema_version":2}`)))
		_, err = repository.Get(ctx, key.Scope, corruptProvider)
		require.True(t, errors.Is(err, ErrCorruptRecord))
	})
}
