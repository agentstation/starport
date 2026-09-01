package apikey

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepositoryContract(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
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
			Metadata:  map[string]any{"account": "account-a"},
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
		require.Len(t, keys, 3)
		primaryData, err := store.Get(ctx, apiKeyStorageKey(apiKey.ID))
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

		listed, err := repository.List(ctx, 10, 0)
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

		initial := apiKey
		initial.ID = "initial-" + suffix
		initial.Name = "initial-key"
		initial.Hash = "initial-hash-" + suffix
		initial.Active = true
		_, err = repository.CreateInitial(ctx, initial)
		require.NoError(t, err)
		require.NoError(t, repository.ReleaseInitial(ctx, initial.ID))
		_, err = repository.CreateInitial(ctx, initial)
		require.NoError(t, err)

		corruptID := "corrupt-" + suffix
		require.NoError(t, store.Set(ctx, apiKeyStorageKey(corruptID), []byte(`{"schema_version":2}`)))
		_, err = repository.GetByID(ctx, corruptID)
		require.True(t, errors.Is(err, ErrCorruptRecord))
	})
}

func TestAPIKeyDeleteUsesStoredHashIndexBytes(t *testing.T) {
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

func TestAPIKeyDeleteRepairsCorruptHashIndex(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	repository, err := Open(store)
	require.NoError(t, err)
	apiKey := APIKey{
		ID: "key-delete-corrupt", Name: "delete-corrupt", Hash: "hash-delete-corrupt",
		Scopes: []string{"chat:write"}, Active: true, CreatedAt: time.Unix(100, 0).UTC(),
	}
	created, err := repository.Create(ctx, apiKey)
	require.NoError(t, err)

	require.NoError(t, store.Set(ctx, hashStorageKey(apiKey.Hash), []byte("not json")))
	// Authentication against the corrupt index fails closed before the repair.
	_, err = repository.GetByHash(ctx, apiKey.Hash)
	require.ErrorIs(t, err, ErrCorruptRecord)

	require.NoError(t, repository.Delete(ctx, apiKey.ID, created.Revision))
	_, err = repository.GetByID(ctx, apiKey.ID)
	require.ErrorIs(t, err, ErrNotFound)
	// The delete repaired the index: the corrupt record left with its owner.
	_, err = store.Get(ctx, hashStorageKey(apiKey.Hash))
	require.ErrorIs(t, err, storage.ErrNotFound)
	_, err = repository.GetByHash(ctx, apiKey.Hash)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAPIKeyDeleteLeavesForeignHashIndex(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	repository, err := Open(store)
	require.NoError(t, err)
	apiKey := APIKey{
		ID: "key-delete-foreign", Name: "delete-foreign", Hash: "hash-delete-foreign",
		Scopes: []string{"chat:write"}, Active: true, CreatedAt: time.Unix(100, 0).UTC(),
	}
	created, err := repository.Create(ctx, apiKey)
	require.NoError(t, err)

	// The index decodes but names another owner. The delete has no claim to
	// remove it, so the owner record leaves and the index stays.
	foreign, err := json.Marshal(hashRecord{SchemaVersion: StorageSchemaVersion, APIKeyID: "another-key"})
	require.NoError(t, err)
	require.NoError(t, store.Set(ctx, hashStorageKey(apiKey.Hash), foreign))

	require.NoError(t, repository.Delete(ctx, apiKey.ID, created.Revision))
	_, err = repository.GetByID(ctx, apiKey.ID)
	require.ErrorIs(t, err, ErrNotFound)
	remaining, err := store.Get(ctx, hashStorageKey(apiKey.Hash))
	require.NoError(t, err)
	require.Equal(t, foreign, remaining)
}

func TestAPIKeyDeleteToleratesMissingHashIndex(t *testing.T) {
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

func TestCreateInitialClaimsRepositoryOnce(t *testing.T) {
	repository, err := Open(storage.NewMockStore())
	require.NoError(t, err)
	first := APIKey{
		ID: "first", Name: "first", Hash: "first-hash", Scopes: []string{"*"},
		Active: true, CreatedAt: time.Now().UTC(),
	}
	_, err = repository.CreateInitial(context.Background(), first)
	require.NoError(t, err)

	second := first
	second.ID = "second"
	second.Name = "second"
	second.Hash = "second-hash"
	_, err = repository.CreateInitial(context.Background(), second)
	require.ErrorIs(t, err, ErrConflict)
	records, err := repository.List(context.Background(), 10, 0)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, first.ID, records[0].APIKey.ID)
}

func TestConcurrentCreatesRetryCollectionContention(t *testing.T) {
	store := &collectionReadBarrierStore{
		KVStore: storage.NewMockStore(),
		ready:   make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	repository, err := Open(store)
	require.NoError(t, err)
	results := make(chan error, 2)
	for index := range 2 {
		index := index
		go func() {
			apiKey := APIKey{
				ID:     "key-" + string(rune('A'+index)),
				Name:   "key-" + string(rune('A'+index)),
				Hash:   "hash-" + string(rune('A'+index)),
				Scopes: []string{"*"}, Active: true, CreatedAt: time.Now().UTC(),
			}
			_, createErr := repository.Create(context.Background(), apiKey)
			results <- createErr
		}()
	}
	<-store.ready
	<-store.ready
	close(store.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent create error = %v", err)
		}
	}
	records, err := repository.List(context.Background(), 2, 0)
	require.NoError(t, err)
	require.Len(t, records, 2)
}

func TestCreateInitialRefusesNonemptyCollection(t *testing.T) {
	repository, err := Open(storage.NewMockStore())
	require.NoError(t, err)
	first := APIKey{
		ID: "first", Name: "first", Hash: "first-hash", Scopes: []string{"*"},
		Active: true, CreatedAt: time.Now().UTC(),
	}
	_, err = repository.Create(context.Background(), first)
	require.NoError(t, err)
	second := first
	second.ID = "second"
	second.Name = "second"
	second.Hash = "second-hash"
	_, err = repository.CreateInitial(context.Background(), second)
	require.ErrorIs(t, err, ErrConflict)
}

func TestCreateInitialReclaimsMissingInitialAPIKey(t *testing.T) {
	repository, err := Open(storage.NewMockStore())
	require.NoError(t, err)
	first := APIKey{
		ID: "first", Name: "first", Hash: "first-hash", Scopes: []string{"*"},
		Active: true, CreatedAt: time.Now().UTC(),
	}
	created, err := repository.CreateInitial(context.Background(), first)
	require.NoError(t, err)
	require.NoError(t, repository.Delete(context.Background(), first.ID, created.Revision))

	second := first
	second.ID = "second"
	second.Name = "second"
	second.Hash = "second-hash"
	_, err = repository.CreateInitial(context.Background(), second)
	require.NoError(t, err)
}

func TestReleaseInitialAllowsSafeRetry(t *testing.T) {
	repository, err := Open(storage.NewMockStore())
	require.NoError(t, err)
	first := APIKey{
		ID: "first", Name: "first", Hash: "first-hash", Scopes: []string{"*"},
		Active: true, CreatedAt: time.Now().UTC(),
	}
	_, err = repository.CreateInitial(context.Background(), first)
	require.NoError(t, err)
	require.NoError(t, repository.ReleaseInitial(context.Background(), first.ID))
	_, err = repository.GetByID(context.Background(), first.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repository.GetByHash(context.Background(), first.Hash)
	require.ErrorIs(t, err, ErrNotFound)

	second := first
	second.ID = "second"
	second.Name = "second"
	second.Hash = "second-hash"
	_, err = repository.CreateInitial(context.Background(), second)
	require.NoError(t, err)
}

func TestReleaseInitialRefusesAnotherAPIKey(t *testing.T) {
	repository, err := Open(storage.NewMockStore())
	require.NoError(t, err)
	first := APIKey{
		ID: "first", Name: "first", Hash: "first-hash", Scopes: []string{"*"},
		Active: true, CreatedAt: time.Now().UTC(),
	}
	_, err = repository.CreateInitial(context.Background(), first)
	require.NoError(t, err)
	require.ErrorIs(t, repository.ReleaseInitial(context.Background(), "other"), ErrConflict)
	_, err = repository.GetByID(context.Background(), first.ID)
	require.NoError(t, err)
}

type collectionReadBarrierStore struct {
	storage.KVStore
	ready   chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func (store *collectionReadBarrierStore) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := store.KVStore.Get(ctx, key)
	if key == collectionKey && store.reads.Add(1) <= 2 {
		store.ready <- struct{}{}
		<-store.release
	}
	return value, err
}
