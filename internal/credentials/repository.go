package credentials

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
	// ProviderCredentialStorageSchemaVersion identifies the only credential schema.
	ProviderCredentialStorageSchemaVersion = 1
	// ProviderCredentialStoragePrefix is the credential v1 namespace.
	ProviderCredentialStoragePrefix = "credentials:v1:"
	defaultListLimit                = 1000
)

var (
	// ErrRepositoryRequired reports an absent credential storage adapter.
	ErrRepositoryRequired = errors.New("credential storage is required")
	// ErrNotFound reports an absent provider credential.
	ErrNotFound = errors.New("provider credential not found")
	// ErrConflict reports a provider-credential revision conflict.
	ErrConflict = errors.New("provider credential revision conflict")
	// ErrCorruptRecord reports invalid durable credential data.
	ErrCorruptRecord = errors.New("provider credential record is invalid")
	// ErrIdentityImmutable reports an attempted scope or provider change.
	ErrIdentityImmutable = errors.New("provider credential identity is immutable")
)

// Record is one versioned provider-credential repository value.
type Record struct {
	Revision uint64
	Key      ProviderKey
}

// Repository is the durable provider-credential contract.
type Repository interface {
	Create(context.Context, ProviderKey) (Record, error)
	Get(context.Context, string, string) (Record, error)
	ListScope(context.Context, string, int) ([]Record, error)
	ListAll(context.Context, int) ([]Record, error)
	Update(context.Context, ProviderKey, uint64) (Record, error)
	Delete(context.Context, string, string, uint64) error
}

type repository struct{ store storage.KVStore }

type providerCredentialRecord struct {
	SchemaVersion int         `json:"schema_version"`
	Revision      uint64      `json:"revision"`
	Key           ProviderKey `json:"provider_key"`
}

// Open returns a storage-backed provider-credential repository.
func Open(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store}, nil
}

func (r *repository) Create(ctx context.Context, key ProviderKey) (Record, error) {
	if err := key.Validate(); err != nil {
		return Record{}, err
	}
	stored := providerCredentialRecord{SchemaVersion: ProviderCredentialStorageSchemaVersion, Revision: 1, Key: cloneProviderKey(key)}
	data, err := json.Marshal(stored)
	if err != nil {
		return Record{}, fmt.Errorf("encode provider credential: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, StorageKey(key.Scope, key.Provider), nil, data); err != nil {
		return Record{}, mapConflict("create provider credential", err)
	}
	return recordFromProviderCredential(stored), nil
}

func (r *repository) Get(ctx context.Context, scope, provider string) (Record, error) {
	if err := validateIdentity(scope, provider); err != nil {
		return Record{}, err
	}
	data, err := r.store.Get(ctx, StorageKey(scope, provider))
	if err != nil {
		return Record{}, mapReadError("get provider credential", err)
	}
	stored, err := decodeProviderCredential(data)
	if err != nil {
		return Record{}, err
	}
	if stored.Key.Scope != scope || stored.Key.Provider != provider {
		return Record{}, fmt.Errorf("%w: identity does not match key", ErrCorruptRecord)
	}
	return recordFromProviderCredential(stored), nil
}

func (r *repository) ListScope(ctx context.Context, scope string, limit int) ([]Record, error) {
	if strings.TrimSpace(scope) == "" {
		return nil, ErrInvalidScope
	}
	return r.list(ctx, ScopePrefix(scope), limit)
}

func (r *repository) ListAll(ctx context.Context, limit int) ([]Record, error) {
	return r.list(ctx, ProviderCredentialStoragePrefix, limit)
}

func (r *repository) list(ctx context.Context, prefix string, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	keys, err := r.store.ScanWithPrefix(ctx, prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("list provider credentials: %w", err)
	}
	sort.Strings(keys)
	records := make([]Record, 0, len(keys))
	for _, storageKey := range keys {
		data, err := r.store.Get(ctx, storageKey)
		if err != nil {
			return nil, mapReadError("read listed provider credential", err)
		}
		stored, err := decodeProviderCredential(data)
		if err != nil {
			return nil, err
		}
		records = append(records, recordFromProviderCredential(stored))
	}
	return records, nil
}

func (r *repository) Update(ctx context.Context, key ProviderKey, expectedRevision uint64) (Record, error) {
	if err := key.Validate(); err != nil {
		return Record{}, err
	}
	storageKey := StorageKey(key.Scope, key.Provider)
	currentData, err := r.store.Get(ctx, storageKey)
	if err != nil {
		return Record{}, mapReadError("get provider credential for update", err)
	}
	current, err := decodeProviderCredential(currentData)
	if err != nil {
		return Record{}, err
	}
	if current.Revision != expectedRevision {
		return Record{}, ErrConflict
	}
	if current.Key.Scope != key.Scope || current.Key.Provider != key.Provider {
		return Record{}, ErrIdentityImmutable
	}
	updated := providerCredentialRecord{
		SchemaVersion: ProviderCredentialStorageSchemaVersion,
		Revision:      current.Revision + 1,
		Key:           cloneProviderKey(key),
	}
	updatedData, err := json.Marshal(updated)
	if err != nil {
		return Record{}, fmt.Errorf("encode provider credential update: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, storageKey, currentData, updatedData); err != nil {
		return Record{}, mapConflict("update provider credential", err)
	}
	return recordFromProviderCredential(updated), nil
}

func (r *repository) Delete(ctx context.Context, scope, provider string, expectedRevision uint64) error {
	if err := validateIdentity(scope, provider); err != nil {
		return err
	}
	storageKey := StorageKey(scope, provider)
	data, err := r.store.Get(ctx, storageKey)
	if err != nil {
		return mapReadError("get provider credential for delete", err)
	}
	stored, err := decodeProviderCredential(data)
	if err != nil {
		return err
	}
	if expectedRevision != 0 && stored.Revision != expectedRevision {
		return ErrConflict
	}
	if err := r.store.CompareAndSwap(ctx, storageKey, data, nil); err != nil {
		return mapConflict("delete provider credential", err)
	}
	return nil
}

// StorageKey returns the canonical key for one scoped provider credential.
func StorageKey(scope, provider string) string {
	return ScopePrefix(scope) + "provider:" + encodePart(provider)
}

// ScopePrefix returns the canonical scan prefix for one credential scope.
func ScopePrefix(scope string) string {
	return ProviderCredentialStoragePrefix + "scope:" + encodePart(scope) + ":"
}

func decodeProviderCredential(data []byte) (providerCredentialRecord, error) {
	var stored providerCredentialRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return providerCredentialRecord{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != ProviderCredentialStorageSchemaVersion || stored.Revision == 0 {
		return providerCredentialRecord{}, fmt.Errorf("%w: unsupported schema or revision", ErrCorruptRecord)
	}
	if err := stored.Key.Validate(); err != nil {
		return providerCredentialRecord{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return stored, nil
}

func validateIdentity(scope, provider string) error {
	if strings.TrimSpace(scope) == "" {
		return ErrInvalidScope
	}
	if strings.TrimSpace(provider) == "" {
		return ErrInvalidProvider
	}
	return nil
}

func encodePart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func recordFromProviderCredential(stored providerCredentialRecord) Record {
	return Record{Revision: stored.Revision, Key: cloneProviderKey(stored.Key)}
}

func cloneProviderKey(key ProviderKey) ProviderKey {
	if key.Config != nil {
		config := make(map[string]any, len(key.Config))
		for name, value := range key.Config {
			config[name] = value
		}
		key.Config = config
	}
	if key.RateLimit != nil {
		rateLimit := *key.RateLimit
		key.RateLimit = &rateLimit
	}
	if key.LastUsed != nil {
		lastUsed := *key.LastUsed
		key.LastUsed = &lastUsed
	}
	return key
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
