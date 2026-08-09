package identity

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
	// StorageSchemaVersion identifies the only supported identity schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the identity v1 storage namespace.
	StoragePrefix = "identity:v1:"

	identityKeyPrefix = StoragePrefix + "key:"
	hashKeyPrefix     = StoragePrefix + "hash:"
	initialKey        = StoragePrefix + "initial"
	collectionKey     = StoragePrefix + "collection"
	defaultListLimit  = 1000
	collectionRetries = 32
)

var (
	// ErrRepositoryRequired reports a missing storage adapter.
	ErrRepositoryRequired = errors.New("identity storage is required")
	// ErrNotFound reports a missing identity.
	ErrNotFound = errors.New("identity not found")
	// ErrConflict reports an existing identity or stale revision.
	ErrConflict = errors.New("identity revision conflict")
	// ErrCorruptRecord reports invalid durable identity data.
	ErrCorruptRecord = errors.New("identity record is invalid")
	// ErrHashImmutable reports an attempted hash mutation.
	ErrHashImmutable = errors.New("identity hash is immutable")
)

// Record is one versioned identity repository value.
type Record struct {
	Revision uint64
	APIKey   APIKey
}

// Repository is the durable identity contract.
type Repository interface {
	Create(context.Context, APIKey) (Record, error)
	CreateInitial(context.Context, APIKey) (Record, error)
	ReleaseInitial(context.Context, string) error
	GetByID(context.Context, string) (Record, error)
	GetByHash(context.Context, string) (Record, error)
	List(context.Context, int) ([]Record, error)
	Update(context.Context, APIKey, uint64) (Record, error)
	Delete(context.Context, string, uint64) error
}

type repository struct {
	store storage.KVStore
}

type identityRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	APIKey        APIKey `json:"api_key"`
}

type hashRecord struct {
	SchemaVersion int    `json:"schema_version"`
	IdentityID    string `json:"identity_id"`
}

type initialIdentityRecord struct {
	SchemaVersion int    `json:"schema_version"`
	IdentityID    string `json:"identity_id"`
}

type identityCollectionRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	Count         uint64 `json:"count"`
}

// Open returns a storage-backed identity repository.
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
		collision, checkErr := r.identityOrHashExists(ctx, apiKey)
		if checkErr != nil {
			return Record{}, checkErr
		}
		if collision {
			return Record{}, ErrConflict
		}
	}
	return Record{}, ErrConflict
}

// CreateInitial atomically claims initialization and creates the first identity.
func (r *repository) CreateInitial(ctx context.Context, apiKey APIKey) (Record, error) {
	return r.create(ctx, apiKey, true)
}

func (r *repository) create(ctx context.Context, apiKey APIKey, initial bool) (Record, error) {
	if err := apiKey.Validate(); err != nil {
		return Record{}, err
	}
	stored := identityRecord{SchemaVersion: StorageSchemaVersion, Revision: 1, APIKey: cloneAPIKey(apiKey)}
	data, err := json.Marshal(stored)
	if err != nil {
		return Record{}, fmt.Errorf("encode identity record: %w", err)
	}
	indexData, err := json.Marshal(hashRecord{SchemaVersion: StorageSchemaVersion, IdentityID: apiKey.ID})
	if err != nil {
		return Record{}, fmt.Errorf("encode identity hash record: %w", err)
	}
	collection, collectionData, err := r.readCollection(ctx)
	if err != nil {
		return Record{}, err
	}
	if initial && collection.Count != 0 {
		return Record{}, ErrConflict
	}
	updatedCollection := identityCollectionRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      collection.Revision + 1,
		Count:         collection.Count + 1,
	}
	updatedCollectionData, err := json.Marshal(updatedCollection)
	if err != nil {
		return Record{}, fmt.Errorf("encode identity collection record: %w", err)
	}

	mutations := []storage.CompareAndSwapMutation{
		{Key: identityStorageKey(apiKey.ID), NewValue: data},
		{Key: hashStorageKey(apiKey.Hash), NewValue: indexData},
		{Key: collectionKey, ExpectedValue: collectionData, NewValue: updatedCollectionData},
	}
	var markerData []byte
	if initial {
		markerData, err = json.Marshal(initialIdentityRecord{
			SchemaVersion: StorageSchemaVersion,
			IdentityID:    apiKey.ID,
		})
		if err != nil {
			return Record{}, fmt.Errorf("encode initial identity claim: %w", err)
		}
		mutations = append([]storage.CompareAndSwapMutation{{
			Key: initialKey, NewValue: markerData,
		}}, mutations...)
	}
	if err := r.store.CompareAndSwapBatch(ctx, mutations); err != nil {
		if initial && errors.Is(err, storage.ErrConflict) {
			return r.replaceMissingInitial(ctx, stored, data, indexData, markerData)
		}
		return Record{}, mapConflict("create identity, hash index, and collection record", err)
	}
	return recordFromIdentity(stored), nil
}

func (r *repository) identityOrHashExists(ctx context.Context, apiKey APIKey) (bool, error) {
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
	stored identityRecord,
	identityData []byte,
	hashData []byte,
	markerData []byte,
) (Record, error) {
	currentMarkerData, err := r.store.Get(ctx, initialKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Record{}, ErrConflict
		}
		return Record{}, mapConflict("create identity and hash index", err)
	}
	currentMarker, err := decodeInitialIdentityRecord(currentMarkerData)
	if err != nil {
		return Record{}, err
	}
	if _, err := r.store.Get(ctx, identityStorageKey(currentMarker.IdentityID)); err == nil {
		return Record{}, ErrConflict
	} else if !errors.Is(err, storage.ErrNotFound) {
		return Record{}, mapReadError("get claimed initial identity", err)
	}
	records, err := r.List(ctx, 1)
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
	updatedCollectionData, err := json.Marshal(identityCollectionRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      collection.Revision + 1,
		Count:         1,
	})
	if err != nil {
		return Record{}, fmt.Errorf("encode replacement identity collection record: %w", err)
	}
	if err := r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
		{Key: initialKey, ExpectedValue: currentMarkerData, NewValue: markerData},
		{Key: identityStorageKey(stored.APIKey.ID), NewValue: identityData},
		{Key: hashStorageKey(stored.APIKey.Hash), NewValue: hashData},
		{Key: collectionKey, ExpectedValue: collectionData, NewValue: updatedCollectionData},
	}); err != nil {
		return Record{}, mapConflict("replace missing initial identity", err)
	}
	return recordFromIdentity(stored), nil
}

// ReleaseInitial atomically deletes the initial identity and its setup claim.
// Call it only when the one-time credential could not be returned.
func (r *repository) ReleaseInitial(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrMissingID
	}
	markerData, err := r.store.Get(ctx, initialKey)
	if err != nil {
		return mapReadError("get initial identity claim", err)
	}
	marker, err := decodeInitialIdentityRecord(markerData)
	if err != nil {
		return err
	}
	if marker.IdentityID != id {
		return ErrConflict
	}
	identityKey := identityStorageKey(id)
	identityData, err := r.store.Get(ctx, identityKey)
	if err != nil {
		return mapReadError("get initial identity", err)
	}
	stored, err := decodeIdentity(identityData)
	if err != nil {
		return err
	}
	if stored.APIKey.ID != id {
		return fmt.Errorf("%w: initial identity ID does not match its key", ErrCorruptRecord)
	}
	hashKey := hashStorageKey(stored.APIKey.Hash)
	hashData, err := r.store.Get(ctx, hashKey)
	if err != nil {
		return mapReadError("get initial identity hash", err)
	}
	index, err := decodeHashRecord(hashData)
	if err != nil {
		return err
	}
	if index.IdentityID != id {
		return fmt.Errorf("%w: initial identity hash target does not match", ErrCorruptRecord)
	}
	collection, collectionData, err := r.readCollection(ctx)
	if err != nil {
		return err
	}
	if collection.Count == 0 {
		return fmt.Errorf("%w: identity collection count is zero", ErrCorruptRecord)
	}
	updatedCollectionData, err := json.Marshal(identityCollectionRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      collection.Revision + 1,
		Count:         collection.Count - 1,
	})
	if err != nil {
		return fmt.Errorf("encode released identity collection record: %w", err)
	}
	if err := r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
		{Key: initialKey, ExpectedValue: markerData},
		{Key: identityKey, ExpectedValue: identityData},
		{Key: hashKey, ExpectedValue: hashData},
		{Key: collectionKey, ExpectedValue: collectionData, NewValue: updatedCollectionData},
	}); err != nil {
		return mapConflict("release initial identity and setup claim", err)
	}
	return nil
}

func (r *repository) GetByID(ctx context.Context, id string) (Record, error) {
	if strings.TrimSpace(id) == "" {
		return Record{}, ErrMissingID
	}
	data, err := r.store.Get(ctx, identityStorageKey(id))
	if err != nil {
		return Record{}, mapReadError("get identity", err)
	}
	stored, err := decodeIdentity(data)
	if err != nil {
		return Record{}, err
	}
	if stored.APIKey.ID != id {
		return Record{}, fmt.Errorf("%w: identity ID does not match its key", ErrCorruptRecord)
	}
	return recordFromIdentity(stored), nil
}

func (r *repository) GetByHash(ctx context.Context, hash string) (Record, error) {
	if strings.TrimSpace(hash) == "" {
		return Record{}, ErrMissingHash
	}
	data, err := r.store.Get(ctx, hashStorageKey(hash))
	if err != nil {
		return Record{}, mapReadError("get identity hash", err)
	}
	index, err := decodeHashRecord(data)
	if err != nil {
		return Record{}, err
	}
	record, err := r.GetByID(ctx, index.IdentityID)
	if err != nil {
		return Record{}, err
	}
	if record.APIKey.Hash != hash {
		return Record{}, fmt.Errorf("%w: hash index target does not match", ErrCorruptRecord)
	}
	return record, nil
}

func (r *repository) List(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	keys, err := r.store.ScanWithPrefix(ctx, identityKeyPrefix, limit)
	if err != nil {
		return nil, fmt.Errorf("list identity keys: %w", err)
	}
	sort.Strings(keys)
	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		data, err := r.store.Get(ctx, key)
		if err != nil {
			return nil, mapReadError("read listed identity", err)
		}
		stored, err := decodeIdentity(data)
		if err != nil {
			return nil, err
		}
		records = append(records, recordFromIdentity(stored))
	}
	return records, nil
}

func (r *repository) Update(ctx context.Context, apiKey APIKey, expectedRevision uint64) (Record, error) {
	if err := apiKey.Validate(); err != nil {
		return Record{}, err
	}
	currentData, err := r.store.Get(ctx, identityStorageKey(apiKey.ID))
	if err != nil {
		return Record{}, mapReadError("get identity for update", err)
	}
	current, err := decodeIdentity(currentData)
	if err != nil {
		return Record{}, err
	}
	if current.Revision != expectedRevision {
		return Record{}, ErrConflict
	}
	if current.APIKey.Hash != apiKey.Hash {
		return Record{}, ErrHashImmutable
	}
	updated := identityRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      current.Revision + 1,
		APIKey:        cloneAPIKey(apiKey),
	}
	updatedData, err := json.Marshal(updated)
	if err != nil {
		return Record{}, fmt.Errorf("encode identity update: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, identityStorageKey(apiKey.ID), currentData, updatedData); err != nil {
		return Record{}, mapConflict("update identity", err)
	}
	return recordFromIdentity(updated), nil
}

func (r *repository) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if strings.TrimSpace(id) == "" {
		return ErrMissingID
	}
	data, err := r.store.Get(ctx, identityStorageKey(id))
	if err != nil {
		return mapReadError("get identity for delete", err)
	}
	stored, err := decodeIdentity(data)
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
		return fmt.Errorf("%w: identity collection count is zero", ErrCorruptRecord)
	}
	updatedCollectionData, err := json.Marshal(identityCollectionRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      collection.Revision + 1,
		Count:         collection.Count - 1,
	})
	if err != nil {
		return fmt.Errorf("encode deleted identity collection record: %w", err)
	}
	indexKey := hashStorageKey(stored.APIKey.Hash)
	indexData, err := r.store.Get(ctx, indexKey)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return mapReadError("get identity hash for delete", err)
	}
	mutations := []storage.CompareAndSwapMutation{
		{Key: identityStorageKey(id), ExpectedValue: data},
		{Key: collectionKey, ExpectedValue: collectionData, NewValue: updatedCollectionData},
	}
	if err == nil {
		index, decodeErr := decodeHashRecord(indexData)
		if decodeErr != nil {
			return decodeErr
		}
		if index.IdentityID != id {
			return fmt.Errorf("%w: hash index target does not match identity", ErrCorruptRecord)
		}
		mutations = append(mutations, storage.CompareAndSwapMutation{Key: indexKey, ExpectedValue: indexData})
	} else {
		// Keep the missing-index observation in the atomic condition. A concurrent
		// repair must make this delete conflict instead of leaving a dangling index.
		mutations = append(mutations, storage.CompareAndSwapMutation{Key: indexKey})
	}
	if err := r.store.CompareAndSwapBatch(ctx, mutations); err != nil {
		return mapConflict("delete identity, hash index, and collection record", err)
	}
	return nil
}

func decodeHashRecord(data []byte) (hashRecord, error) {
	var index hashRecord
	if err := json.Unmarshal(data, &index); err != nil ||
		index.SchemaVersion != StorageSchemaVersion || index.IdentityID == "" {
		return hashRecord{}, fmt.Errorf("%w: hash index", ErrCorruptRecord)
	}
	return index, nil
}

func decodeInitialIdentityRecord(data []byte) (initialIdentityRecord, error) {
	var record initialIdentityRecord
	if err := json.Unmarshal(data, &record); err != nil ||
		record.SchemaVersion != StorageSchemaVersion || record.IdentityID == "" {
		return initialIdentityRecord{}, fmt.Errorf("%w: initial identity claim", ErrCorruptRecord)
	}
	return record, nil
}

func (r *repository) readCollection(
	ctx context.Context,
) (identityCollectionRecord, []byte, error) {
	data, err := r.store.Get(ctx, collectionKey)
	if errors.Is(err, storage.ErrNotFound) {
		return identityCollectionRecord{SchemaVersion: StorageSchemaVersion}, nil, nil
	}
	if err != nil {
		return identityCollectionRecord{}, nil, mapReadError("get identity collection record", err)
	}
	record, err := decodeIdentityCollectionRecord(data)
	if err != nil {
		return identityCollectionRecord{}, nil, err
	}
	return record, data, nil
}

func decodeIdentityCollectionRecord(data []byte) (identityCollectionRecord, error) {
	var record identityCollectionRecord
	if err := json.Unmarshal(data, &record); err != nil ||
		record.SchemaVersion != StorageSchemaVersion || record.Revision == 0 {
		return identityCollectionRecord{}, fmt.Errorf("%w: identity collection record", ErrCorruptRecord)
	}
	return record, nil
}

func decodeIdentity(data []byte) (identityRecord, error) {
	var stored identityRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return identityRecord{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion || stored.Revision == 0 {
		return identityRecord{}, fmt.Errorf("%w: unsupported schema or revision", ErrCorruptRecord)
	}
	if err := stored.APIKey.Validate(); err != nil {
		return identityRecord{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return stored, nil
}

func identityStorageKey(id string) string { return identityKeyPrefix + encodePart(id) }
func hashStorageKey(hash string) string   { return hashKeyPrefix + encodePart(hash) }

func encodePart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func recordFromIdentity(stored identityRecord) Record {
	return Record{Revision: stored.Revision, APIKey: cloneAPIKey(stored.APIKey)}
}

func cloneAPIKey(apiKey APIKey) APIKey {
	apiKey.Scopes = append([]string(nil), apiKey.Scopes...)
	apiKey.AllowedModels = append([]string(nil), apiKey.AllowedModels...)
	apiKey.RateLimitConfig = cloneMap(apiKey.RateLimitConfig)
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
