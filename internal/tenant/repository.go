package tenant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

const (
	// StorageSchemaVersion identifies the only supported tenant schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the tenant v1 storage namespace.
	StoragePrefix = "tenant:v1:"

	tenantKeyPrefix  = StoragePrefix + "tenant:"
	defaultListLimit = 1000
)

var (
	// ErrRepositoryRequired reports a missing storage adapter.
	ErrRepositoryRequired = errors.New("tenant storage is required")
	// ErrNotFound reports a missing tenant.
	ErrNotFound = errors.New("tenant not found")
	// ErrConflict reports an existing tenant or a stale revision.
	ErrConflict = errors.New("tenant revision conflict")
	// ErrCorruptRecord reports invalid durable tenant data.
	ErrCorruptRecord = errors.New("tenant record is invalid")
	// ErrDefaultImmutable reports an attempt to delete the canonical tenant.
	ErrDefaultImmutable = errors.New("the default tenant cannot be deleted")
)

// Record is one versioned tenant repository value.
type Record struct {
	Revision uint64
	Tenant   Tenant
}

// Repository is the durable tenant contract.
type Repository interface {
	Create(context.Context, Tenant) (Record, error)
	EnsureDefault(context.Context) (Record, error)
	GetByID(context.Context, string) (Record, error)
	List(context.Context, int, int) ([]Record, error)
	Update(context.Context, Tenant, uint64) (Record, error)
	Delete(context.Context, string, uint64) error
}

type repository struct {
	store storage.KVStore
	now   func() time.Time
}

type tenantRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	Tenant        Tenant `json:"tenant"`
}

// Open returns a storage-backed tenant repository.
func Open(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store, now: time.Now}, nil
}

func (r *repository) Create(ctx context.Context, value Tenant) (Record, error) {
	if err := value.Validate(); err != nil {
		return Record{}, err
	}
	stored := tenantRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      1,
		Tenant:        cloneTenant(value),
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return Record{}, fmt.Errorf("encode tenant record: %w", err)
	}
	// A nil ExpectedValue on an absent key creates. On a present key it
	// conflicts, so two concurrent creates cannot both win.
	if err := r.store.CompareAndSwap(ctx, tenantStorageKey(value.ID), nil, data); err != nil {
		return Record{}, mapConflict("create tenant", err)
	}
	return Record{Revision: stored.Revision, Tenant: stored.Tenant}, nil
}

// EnsureDefault creates the canonical tenant once and is safe to call on every
// boot and from concurrent processes. A create that loses the race reads the
// winner rather than failing startup.
func (r *repository) EnsureDefault(ctx context.Context) (Record, error) {
	existing, err := r.GetByID(ctx, DefaultID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Record{}, err
	}
	created := r.now().UTC()
	// The strategy is written rather than left empty so the stored record
	// states the policy instead of relying on a zero-value reading.
	record, err := r.Create(ctx, Tenant{
		ID:                 DefaultID,
		Name:               DefaultName,
		CredentialStrategy: StrategyOperatorFirst,
		Active:             true,
		CreatedAt:          created,
		UpdatedAt:          created,
	})
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, ErrConflict) {
		return Record{}, err
	}
	return r.GetByID(ctx, DefaultID)
}

func (r *repository) GetByID(ctx context.Context, id string) (Record, error) {
	if strings.TrimSpace(id) == "" {
		return Record{}, ErrMissingID
	}
	data, err := r.store.Get(ctx, tenantStorageKey(id))
	if err != nil {
		return Record{}, mapReadError("get tenant", err)
	}
	stored, err := decodeTenant(data)
	if err != nil {
		return Record{}, err
	}
	if stored.Tenant.ID != id {
		return Record{}, fmt.Errorf("%w: tenant ID does not match its key", ErrCorruptRecord)
	}
	return Record{Revision: stored.Revision, Tenant: stored.Tenant}, nil
}

func (r *repository) List(ctx context.Context, limit, offset int) ([]Record, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if offset < 0 {
		offset = 0
	}
	// The store scan order is not a contract, so stable pagination needs the
	// full sorted key set before slicing.
	keys, err := r.store.ScanWithPrefix(ctx, tenantKeyPrefix, 0)
	if err != nil {
		return nil, fmt.Errorf("list tenant keys: %w", err)
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
			return nil, mapReadError("read listed tenant", err)
		}
		stored, err := decodeTenant(data)
		if err != nil {
			return nil, err
		}
		records = append(records, Record{Revision: stored.Revision, Tenant: stored.Tenant})
	}
	return records, nil
}

func (r *repository) Update(ctx context.Context, value Tenant, expectedRevision uint64) (Record, error) {
	if err := ValidateID(value.ID); err != nil {
		return Record{}, err
	}
	currentData, err := r.store.Get(ctx, tenantStorageKey(value.ID))
	if err != nil {
		return Record{}, mapReadError("get tenant for update", err)
	}
	current, err := decodeTenant(currentData)
	if err != nil {
		return Record{}, err
	}
	if current.Revision != expectedRevision {
		return Record{}, ErrConflict
	}
	updatedTenant := cloneTenant(value)
	// Creation time is a property of the record, not of the caller's payload.
	// The timestamps are stamped before validation so the check reads the
	// record this call actually writes.
	updatedTenant.CreatedAt = current.Tenant.CreatedAt
	updatedTenant.UpdatedAt = r.now().UTC()
	if err := updatedTenant.Validate(); err != nil {
		return Record{}, err
	}
	updated := tenantRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      current.Revision + 1,
		Tenant:        updatedTenant,
	}
	updatedData, err := json.Marshal(updated)
	if err != nil {
		return Record{}, fmt.Errorf("encode tenant update: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, tenantStorageKey(value.ID), currentData, updatedData); err != nil {
		return Record{}, mapConflict("update tenant", err)
	}
	return Record{Revision: updated.Revision, Tenant: updated.Tenant}, nil
}

func (r *repository) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if strings.TrimSpace(id) == "" {
		return ErrMissingID
	}
	// Every gateway API key resolves to a tenant, and a key with no explicit
	// tenant resolves to this one. Removing it would strand those keys.
	if id == DefaultID {
		return ErrDefaultImmutable
	}
	data, err := r.store.Get(ctx, tenantStorageKey(id))
	if err != nil {
		return mapReadError("get tenant for delete", err)
	}
	stored, err := decodeTenant(data)
	if err != nil {
		return err
	}
	if expectedRevision != 0 && stored.Revision != expectedRevision {
		return ErrConflict
	}
	if err := r.store.CompareAndSwap(ctx, tenantStorageKey(id), data, nil); err != nil {
		return mapConflict("delete tenant", err)
	}
	return nil
}

func decodeTenant(data []byte) (tenantRecord, error) {
	var stored tenantRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return tenantRecord{}, fmt.Errorf("%w: %s", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion {
		return tenantRecord{}, fmt.Errorf(
			"%w: unsupported schema version %d",
			ErrCorruptRecord,
			stored.SchemaVersion,
		)
	}
	if stored.Revision == 0 {
		return tenantRecord{}, fmt.Errorf("%w: tenant revision is zero", ErrCorruptRecord)
	}
	if err := stored.Tenant.Validate(); err != nil {
		return tenantRecord{}, fmt.Errorf("%w: %s", ErrCorruptRecord, err)
	}
	return stored, nil
}

func tenantStorageKey(id string) string { return tenantKeyPrefix + encodePart(id) }

func encodePart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func cloneTenant(value Tenant) Tenant {
	clone := value
	clone.Limits = value.Limits.Clone()
	clone.Metadata = cloneMap(value.Metadata)
	return clone
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
