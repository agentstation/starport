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
	defaultListLimit  = 1000
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

// Open returns a storage-backed identity repository.
func Open(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store}, nil
}

func (r *repository) Create(ctx context.Context, apiKey APIKey) (Record, error) {
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

	if err := r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
		{Key: identityStorageKey(apiKey.ID), NewValue: data},
		{Key: hashStorageKey(apiKey.Hash), NewValue: indexData},
	}); err != nil {
		return Record{}, mapConflict("create identity and hash index", err)
	}
	return recordFromIdentity(stored), nil
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
	indexKey := hashStorageKey(stored.APIKey.Hash)
	indexData, err := r.store.Get(ctx, indexKey)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return mapReadError("get identity hash for delete", err)
	}
	mutations := []storage.CompareAndSwapMutation{
		{Key: identityStorageKey(id), ExpectedValue: data},
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
		return mapConflict("delete identity and hash index", err)
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
