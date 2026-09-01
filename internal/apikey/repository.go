package apikey

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

const (
	// StorageSchemaVersion identifies the only supported API key schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the API key v1 storage namespace.
	StoragePrefix = "identity:v1:"

	apiKeyPrefix      = StoragePrefix + "key:"
	hashKeyPrefix     = StoragePrefix + "hash:"
	initialKey        = StoragePrefix + "initial"
	collectionKey     = StoragePrefix + "collection"
	defaultListLimit  = 1000
	collectionRetries = 32
)

var (
	// ErrRepositoryRequired reports a missing storage adapter.
	ErrRepositoryRequired = errors.New("API key storage is required")
	// ErrNotFound reports a missing API key.
	ErrNotFound = errors.New("API key not found")
	// ErrConflict reports an existing API key or stale revision.
	ErrConflict = errors.New("API key revision conflict")
	// ErrCorruptRecord reports invalid durable API key data.
	ErrCorruptRecord = errors.New("API key record is invalid")
	// ErrHashImmutable reports an attempted hash mutation.
	ErrHashImmutable = errors.New("API key hash is immutable")
)

// Record is one versioned API key repository value.
type Record struct {
	Revision uint64
	APIKey   APIKey
}

// Repository is the durable API key contract.
type Repository interface {
	Create(context.Context, APIKey) (Record, error)
	CreateInitial(context.Context, APIKey) (Record, error)
	ReleaseInitial(context.Context, string) error
	GetByID(context.Context, string) (Record, error)
	GetByHash(context.Context, string) (Record, error)
	List(context.Context, int, int) ([]Record, error)
	Update(context.Context, APIKey, uint64) (Record, error)
	Delete(context.Context, string, uint64) error
}

type repository struct {
	store storage.KVStore
}

type apiKeyRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	APIKey        APIKey `json:"api_key"`
}

type hashRecord struct {
	SchemaVersion int    `json:"schema_version"`
	APIKeyID      string `json:"identity_id"`
}

type initialAPIKeyRecord struct {
	SchemaVersion int    `json:"schema_version"`
	APIKeyID      string `json:"identity_id"`
}

type apiKeyCollectionRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	Count         uint64 `json:"count"`
}

// Open returns a storage-backed API key repository.
func Open(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store}, nil
}

func (r *repository) Create(ctx context.Context, apiKey APIKey) (Record, error) {
	for range collectionRetries {
		record, err := r.create(ctx, apiKey, false)
		if !errors.Is(err, ErrConflict) {
			return record, err
		}
		collision, checkErr := r.apiKeyOrHashExists(ctx, apiKey)
		if checkErr != nil {
			return Record{}, checkErr
		}
		if collision {
			return Record{}, ErrConflict
		}
	}
	return Record{}, ErrConflict
}

// CreateInitial atomically claims initialization and creates the first API key.
func (r *repository) CreateInitial(ctx context.Context, apiKey APIKey) (Record, error) {
	return r.create(ctx, apiKey, true)
}

func (r *repository) create(ctx context.Context, apiKey APIKey, initial bool) (Record, error) {
	if err := apiKey.Validate(); err != nil {
		return Record{}, err
	}
	stored := apiKeyRecord{SchemaVersion: StorageSchemaVersion, Revision: 1, APIKey: cloneAPIKey(apiKey)}
	data, err := json.Marshal(stored)
	if err != nil {
		return Record{}, fmt.Errorf("encode API key record: %w", err)
	}
	indexData, err := json.Marshal(hashRecord{SchemaVersion: StorageSchemaVersion, APIKeyID: apiKey.ID})
	if err != nil {
		return Record{}, fmt.Errorf("encode API key hash record: %w", err)
	}
	collection, collectionData, err := r.readCollection(ctx)
	if err != nil {
		return Record{}, err
	}
	if initial && collection.Count != 0 {
		return Record{}, ErrConflict
	}
	updatedCollection := apiKeyCollectionRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      collection.Revision + 1,
		Count:         collection.Count + 1,
	}
	updatedCollectionData, err := json.Marshal(updatedCollection)
	if err != nil {
		return Record{}, fmt.Errorf("encode API key collection record: %w", err)
	}

	mutations := []storage.CompareAndSwapMutation{
		{Key: apiKeyStorageKey(apiKey.ID), NewValue: data},
		{Key: hashStorageKey(apiKey.Hash), NewValue: indexData},
		{Key: collectionKey, ExpectedValue: collectionData, NewValue: updatedCollectionData},
	}
	var markerData []byte
	if initial {
		markerData, err = json.Marshal(initialAPIKeyRecord{
			SchemaVersion: StorageSchemaVersion,
			APIKeyID:      apiKey.ID,
		})
		if err != nil {
			return Record{}, fmt.Errorf("encode initial API key claim: %w", err)
		}
		mutations = append([]storage.CompareAndSwapMutation{{
			Key: initialKey, NewValue: markerData,
		}}, mutations...)
	}
	if err := r.store.CompareAndSwapBatch(ctx, mutations); err != nil {
		if initial && errors.Is(err, storage.ErrConflict) {
			return r.replaceMissingInitial(ctx, stored, data, indexData, markerData)
		}
		return Record{}, mapConflict("create API key, hash index, and collection record", err)
	}
	return recordFromStored(stored), nil
}

func (r *repository) apiKeyOrHashExists(ctx context.Context, apiKey APIKey) (bool, error) {
	if _, err := r.GetByID(ctx, apiKey.ID); err == nil {
		return true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if _, err := r.GetByHash(ctx, apiKey.Hash); err == nil {
		return true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (r *repository) replaceMissingInitial(
	ctx context.Context,
	stored apiKeyRecord,
	apiKeyData []byte,
	hashData []byte,
	markerData []byte,
) (Record, error) {
	currentMarkerData, err := r.store.Get(ctx, initialKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Record{}, ErrConflict
		}
		return Record{}, mapConflict("create API key and hash index", err)
	}
	currentMarker, err := decodeInitialAPIKeyRecord(currentMarkerData)
	if err != nil {
		return Record{}, err
	}
	if _, err := r.store.Get(ctx, apiKeyStorageKey(currentMarker.APIKeyID)); err == nil {
		return Record{}, ErrConflict
	} else if !errors.Is(err, storage.ErrNotFound) {
		return Record{}, mapReadError("get claimed initial API key", err)
	}
	records, err := r.List(ctx, 1, 0)
	if err != nil {
		return Record{}, err
	}
	if len(records) != 0 {
		return Record{}, ErrConflict
	}
	collection, collectionData, err := r.readCollection(ctx)
	if err != nil {
		return Record{}, err
	}
	if collection.Count != 0 {
		return Record{}, ErrConflict
	}
	updatedCollectionData, err := json.Marshal(apiKeyCollectionRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      collection.Revision + 1,
		Count:         1,
	})
	if err != nil {
		return Record{}, fmt.Errorf("encode replacement API key collection record: %w", err)
	}
	if err := r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
		{Key: initialKey, ExpectedValue: currentMarkerData, NewValue: markerData},
		{Key: apiKeyStorageKey(stored.APIKey.ID), NewValue: apiKeyData},
		{Key: hashStorageKey(stored.APIKey.Hash), NewValue: hashData},
		{Key: collectionKey, ExpectedValue: collectionData, NewValue: updatedCollectionData},
	}); err != nil {
		return Record{}, mapConflict("replace missing initial API key", err)
	}
	return recordFromStored(stored), nil
}

// ReleaseInitial atomically deletes the initial API key and its setup claim.
// Call it only when the one-time credential could not be returned.
func (r *repository) ReleaseInitial(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrMissingID
	}
	markerData, err := r.store.Get(ctx, initialKey)
	if err != nil {
		return mapReadError("get initial API key claim", err)
	}
	marker, err := decodeInitialAPIKeyRecord(markerData)
	if err != nil {
		return err
	}
	if marker.APIKeyID != id {
		return ErrConflict
	}
	apiKeyStoreKey := apiKeyStorageKey(id)
	apiKeyData, err := r.store.Get(ctx, apiKeyStoreKey)
	if err != nil {
		return mapReadError("get initial API key", err)
	}
	stored, err := decodeAPIKey(apiKeyData)
	if err != nil {
		return err
	}
	if stored.APIKey.ID != id {
		return fmt.Errorf("%w: initial API key ID does not match its key", ErrCorruptRecord)
	}
	hashKey := hashStorageKey(stored.APIKey.Hash)
	hashData, err := r.store.Get(ctx, hashKey)
	if err != nil {
		return mapReadError("get initial API key hash", err)
	}
	index, err := decodeHashRecord(hashData)
	if err != nil {
		return err
	}
	if index.APIKeyID != id {
		return fmt.Errorf("%w: initial API key hash target does not match", ErrCorruptRecord)
	}
	collection, collectionData, err := r.readCollection(ctx)
	if err != nil {
		return err
	}
	if collection.Count == 0 {
		return fmt.Errorf("%w: API key collection count is zero", ErrCorruptRecord)
	}
	updatedCollectionData, err := json.Marshal(apiKeyCollectionRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      collection.Revision + 1,
		Count:         collection.Count - 1,
	})
	if err != nil {
		return fmt.Errorf("encode released API key collection record: %w", err)
	}
	if err := r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
		{Key: initialKey, ExpectedValue: markerData},
		{Key: apiKeyStoreKey, ExpectedValue: apiKeyData},
		{Key: hashKey, ExpectedValue: hashData},
		{Key: collectionKey, ExpectedValue: collectionData, NewValue: updatedCollectionData},
	}); err != nil {
		return mapConflict("release initial API key and setup claim", err)
	}
	return nil
}

func (r *repository) GetByID(ctx context.Context, id string) (Record, error) {
	if strings.TrimSpace(id) == "" {
		return Record{}, ErrMissingID
	}
	data, err := r.store.Get(ctx, apiKeyStorageKey(id))
	if err != nil {
		return Record{}, mapReadError("get API key", err)
	}
	stored, err := decodeAPIKey(data)
	if err != nil {
		return Record{}, err
	}
	if stored.APIKey.ID != id {
		return Record{}, fmt.Errorf("%w: API key ID does not match its key", ErrCorruptRecord)
	}
	return recordFromStored(stored), nil
}

func (r *repository) GetByHash(ctx context.Context, hash string) (Record, error) {
	if strings.TrimSpace(hash) == "" {
		return Record{}, ErrMissingHash
	}
	data, err := r.store.Get(ctx, hashStorageKey(hash))
	if err != nil {
		return Record{}, mapReadError("get API key hash", err)
	}
	index, err := decodeHashRecord(data)
	if err != nil {
		return Record{}, err
	}
	record, err := r.GetByID(ctx, index.APIKeyID)
	if err != nil {
		return Record{}, err
	}
	if record.APIKey.Hash != hash {
		return Record{}, fmt.Errorf("%w: hash index target does not match", ErrCorruptRecord)
	}
	return record, nil
}

func (r *repository) List(ctx context.Context, limit, offset int) ([]Record, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if offset < 0 {
		offset = 0
	}
	// Scan every API key key: the store scan order is not a contract, so
	// stable pagination needs the full sorted key set before slicing.
	keys, err := r.store.ScanWithPrefix(ctx, apiKeyPrefix, 0)
	if err != nil {
		return nil, fmt.Errorf("list API key keys: %w", err)
	}
	sort.Strings(keys)
	if offset >= len(keys) {
		return []Record{}, nil
	}
	keys = keys[offset:]
	if len(keys) > limit {
		keys = keys[:limit]
	}
	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		data, err := r.store.Get(ctx, key)
		if err != nil {
			return nil, mapReadError("read listed API key", err)
		}
		stored, err := decodeAPIKey(data)
		if err != nil {
			return nil, err
		}
		records = append(records, recordFromStored(stored))
	}
	return records, nil
}

func (r *repository) Update(ctx context.Context, apiKey APIKey, expectedRevision uint64) (Record, error) {
	if err := apiKey.Validate(); err != nil {
		return Record{}, err
	}
	currentData, err := r.store.Get(ctx, apiKeyStorageKey(apiKey.ID))
	if err != nil {
		return Record{}, mapReadError("get API key for update", err)
	}
	current, err := decodeAPIKey(currentData)
	if err != nil {
		return Record{}, err
	}
	if current.Revision != expectedRevision {
		return Record{}, ErrConflict
	}
	if current.APIKey.Hash != apiKey.Hash {
		return Record{}, ErrHashImmutable
	}
	updated := apiKeyRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      current.Revision + 1,
		APIKey:        cloneAPIKey(apiKey),
	}
	updatedData, err := json.Marshal(updated)
	if err != nil {
		return Record{}, fmt.Errorf("encode API key update: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, apiKeyStorageKey(apiKey.ID), currentData, updatedData); err != nil {
		return Record{}, mapConflict("update API key", err)
	}
	return recordFromStored(updated), nil
}

func (r *repository) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if strings.TrimSpace(id) == "" {
		return ErrMissingID
	}
	data, err := r.store.Get(ctx, apiKeyStorageKey(id))
	if err != nil {
		return mapReadError("get API key for delete", err)
	}
	stored, err := decodeAPIKey(data)
	if err != nil {
		return err
	}
	if expectedRevision != 0 && stored.Revision != expectedRevision {
		return ErrConflict
	}
	collection, collectionData, err := r.readCollection(ctx)
	if err != nil {
		return err
	}
	if collection.Count == 0 {
		return fmt.Errorf("%w: API key collection count is zero", ErrCorruptRecord)
	}
	updatedCollectionData, err := json.Marshal(apiKeyCollectionRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      collection.Revision + 1,
		Count:         collection.Count - 1,
	})
	if err != nil {
		return fmt.Errorf("encode deleted API key collection record: %w", err)
	}
	indexKey := hashStorageKey(stored.APIKey.Hash)
	indexData, err := r.store.Get(ctx, indexKey)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return mapReadError("get API key hash for delete", err)
	}
	mutations := []storage.CompareAndSwapMutation{
		{Key: apiKeyStorageKey(id), ExpectedValue: data},
		{Key: collectionKey, ExpectedValue: collectionData, NewValue: updatedCollectionData},
	}
	if err == nil {
		index, decodeErr := decodeHashRecord(indexData)
		switch {
		case decodeErr != nil:
			// A corrupt index serves nobody: authentication against it already
			// fails closed, so the owner's delete repairs it by removing it.
			mutations = append(mutations, storage.CompareAndSwapMutation{Key: indexKey, ExpectedValue: indexData})
		case index.APIKeyID != id:
			// A foreign index names another owner, so this delete has no claim
			// to remove it. Hold it unchanged so a concurrent rewrite conflicts.
			mutations = append(mutations, storage.CompareAndSwapMutation{
				Key: indexKey, ExpectedValue: indexData, NewValue: indexData,
			})
		default:
			mutations = append(mutations, storage.CompareAndSwapMutation{Key: indexKey, ExpectedValue: indexData})
		}
	} else {
		// Keep the missing-index observation in the atomic condition. A concurrent
		// repair must make this delete conflict instead of leaving a dangling index.
		mutations = append(mutations, storage.CompareAndSwapMutation{Key: indexKey})
	}
	if err := r.store.CompareAndSwapBatch(ctx, mutations); err != nil {
		return mapConflict("delete API key, hash index, and collection record", err)
	}
	return nil
}

func decodeHashRecord(data []byte) (hashRecord, error) {
	var index hashRecord
	if err := json.Unmarshal(data, &index); err != nil ||
		index.SchemaVersion != StorageSchemaVersion || index.APIKeyID == "" {
		return hashRecord{}, fmt.Errorf("%w: hash index", ErrCorruptRecord)
	}
	return index, nil
}

func decodeInitialAPIKeyRecord(data []byte) (initialAPIKeyRecord, error) {
	var record initialAPIKeyRecord
	if err := json.Unmarshal(data, &record); err != nil ||
		record.SchemaVersion != StorageSchemaVersion || record.APIKeyID == "" {
		return initialAPIKeyRecord{}, fmt.Errorf("%w: initial API key claim", ErrCorruptRecord)
	}
	return record, nil
}

func (r *repository) readCollection(
	ctx context.Context,
) (apiKeyCollectionRecord, []byte, error) {
	data, err := r.store.Get(ctx, collectionKey)
	if errors.Is(err, storage.ErrNotFound) {
		return apiKeyCollectionRecord{SchemaVersion: StorageSchemaVersion}, nil, nil
	}
	if err != nil {
		return apiKeyCollectionRecord{}, nil, mapReadError("get API key collection record", err)
	}
	record, err := decodeAPIKeyCollectionRecord(data)
	if err != nil {
		return apiKeyCollectionRecord{}, nil, err
	}
	return record, data, nil
}

func decodeAPIKeyCollectionRecord(data []byte) (apiKeyCollectionRecord, error) {
	var record apiKeyCollectionRecord
	if err := json.Unmarshal(data, &record); err != nil ||
		record.SchemaVersion != StorageSchemaVersion || record.Revision == 0 {
		return apiKeyCollectionRecord{}, fmt.Errorf("%w: API key collection record", ErrCorruptRecord)
	}
	return record, nil
}

func decodeAPIKey(data []byte) (apiKeyRecord, error) {
	var stored apiKeyRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return apiKeyRecord{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion || stored.Revision == 0 {
		return apiKeyRecord{}, fmt.Errorf("%w: unsupported schema or revision", ErrCorruptRecord)
	}
	if err := stored.APIKey.Validate(); err != nil {
		return apiKeyRecord{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return stored, nil
}

func apiKeyStorageKey(id string) string { return apiKeyPrefix + encodePart(id) }
func hashStorageKey(hash string) string { return hashKeyPrefix + encodePart(hash) }

func encodePart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func recordFromStored(stored apiKeyRecord) Record {
	return Record{Revision: stored.Revision, APIKey: cloneAPIKey(stored.APIKey)}
}

func cloneAPIKey(apiKey APIKey) APIKey {
	apiKey.Scopes = append([]string(nil), apiKey.Scopes...)
	apiKey.AllowedModels = append([]string(nil), apiKey.AllowedModels...)
	apiKey.Limits = apiKey.Limits.Clone()
	apiKey.Metadata = cloneMap(apiKey.Metadata)
	if apiKey.ExpiresAt != nil {
		expiresAt := *apiKey.ExpiresAt
		apiKey.ExpiresAt = &expiresAt
	}
	return apiKey
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func mapReadError(action string, err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", action, err)
}

func mapConflict(action string, err error) error {
	if errors.Is(err, storage.ErrConflict) {
		return ErrConflict
	}
	return fmt.Errorf("%s: %w", action, err)
}
