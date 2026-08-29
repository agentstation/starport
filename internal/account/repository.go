package account

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
	// StorageSchemaVersion identifies the only supported account schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the account v1 storage namespace.
	StoragePrefix = "account:v1:"

	accountKeyPrefix = StoragePrefix + "account:"
	defaultListLimit = 1000
)

var (
	// ErrRepositoryRequired reports a missing storage adapter.
	ErrRepositoryRequired = errors.New("account storage is required")
	// ErrNotFound reports a missing account.
	ErrNotFound = errors.New("account not found")
	// ErrConflict reports an existing account or a stale revision.
	ErrConflict = errors.New("account revision conflict")
	// ErrCorruptRecord reports invalid durable account data.
	ErrCorruptRecord = errors.New("account record is invalid")
	// ErrDefaultImmutable reports an attempt to delete the canonical account.
	ErrDefaultImmutable = errors.New("the default account cannot be deleted")
)

// Record is one versioned account repository value.
type Record struct {
	Revision uint64
	Account  Account
}

// Repository is the durable account contract.
type Repository interface {
	Create(context.Context, Account) (Record, error)
	EnsureDefault(context.Context) (Record, error)
	GetByID(context.Context, string) (Record, error)
	Exists(context.Context, string) (bool, error)
	List(context.Context, int, int) ([]Record, error)
	Update(context.Context, Account, uint64) (Record, error)
	Delete(context.Context, string, uint64) error
}

type repository struct {
	store storage.KVStore
	now   func() time.Time
}

type accountRecord struct {
	SchemaVersion int     `json:"schema_version"`
	Revision      uint64  `json:"revision"`
	Account       Account `json:"account"`
}

// Open returns a storage-backed account repository.
func Open(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store, now: time.Now}, nil
}

func (r *repository) Create(ctx context.Context, value Account) (Record, error) {
	if err := value.Validate(); err != nil {
		return Record{}, err
	}
	stored := accountRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      1,
		Account:       cloneAccount(value),
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return Record{}, fmt.Errorf("encode account record: %w", err)
	}
	// A nil ExpectedValue on an absent key creates. On a present key it
	// conflicts, so two concurrent creates cannot both win.
	if err := r.store.CompareAndSwap(ctx, accountStorageKey(value.ID), nil, data); err != nil {
		return Record{}, mapConflict("create account", err)
	}
	return Record{Revision: stored.Revision, Account: stored.Account}, nil
}

// EnsureDefault creates the canonical account once and is safe to call on every
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
	record, err := r.Create(ctx, Account{
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
	data, err := r.store.Get(ctx, accountStorageKey(id))
	if err != nil {
		return Record{}, mapReadError("get account", err)
	}
	stored, err := decodeAccount(data)
	if err != nil {
		return Record{}, err
	}
	if stored.Account.ID != id {
		return Record{}, fmt.Errorf("%w: account ID does not match its key", ErrCorruptRecord)
	}
	return Record{Revision: stored.Revision, Account: stored.Account}, nil
}

// Exists reports whether an account is present. A caller that only needs the
// answer should not have to distinguish ErrNotFound from a storage failure.
func (r *repository) Exists(ctx context.Context, id string) (bool, error) {
	if _, err := r.GetByID(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
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
	keys, err := r.store.ScanWithPrefix(ctx, accountKeyPrefix, 0)
	if err != nil {
		return nil, fmt.Errorf("list account keys: %w", err)
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
			return nil, mapReadError("read listed account", err)
		}
		stored, err := decodeAccount(data)
		if err != nil {
			return nil, err
		}
		records = append(records, Record{Revision: stored.Revision, Account: stored.Account})
	}
	return records, nil
}

func (r *repository) Update(ctx context.Context, value Account, expectedRevision uint64) (Record, error) {
	if err := ValidateID(value.ID); err != nil {
		return Record{}, err
	}
	currentData, err := r.store.Get(ctx, accountStorageKey(value.ID))
	if err != nil {
		return Record{}, mapReadError("get account for update", err)
	}
	current, err := decodeAccount(currentData)
	if err != nil {
		return Record{}, err
	}
	if current.Revision != expectedRevision {
		return Record{}, ErrConflict
	}
	updatedAccount := cloneAccount(value)
	// Creation time is a property of the record, not of the caller's payload.
	// The timestamps are stamped before validation so the check reads the
	// record this call actually writes.
	updatedAccount.CreatedAt = current.Account.CreatedAt
	updatedAccount.UpdatedAt = r.now().UTC()
	if err := updatedAccount.Validate(); err != nil {
		return Record{}, err
	}
	updated := accountRecord{
		SchemaVersion: StorageSchemaVersion,
		Revision:      current.Revision + 1,
		Account:       updatedAccount,
	}
	updatedData, err := json.Marshal(updated)
	if err != nil {
		return Record{}, fmt.Errorf("encode account update: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, accountStorageKey(value.ID), currentData, updatedData); err != nil {
		return Record{}, mapConflict("update account", err)
	}
	return Record{Revision: updated.Revision, Account: updated.Account}, nil
}

func (r *repository) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if strings.TrimSpace(id) == "" {
		return ErrMissingID
	}
	// Every gateway API key resolves to an account, and a key with no explicit
	// account resolves to this one. Removing it would strand those keys.
	if id == DefaultID {
		return ErrDefaultImmutable
	}
	data, err := r.store.Get(ctx, accountStorageKey(id))
	if err != nil {
		return mapReadError("get account for delete", err)
	}
	stored, err := decodeAccount(data)
	if err != nil {
		return err
	}
	if expectedRevision != 0 && stored.Revision != expectedRevision {
		return ErrConflict
	}
	if err := r.store.CompareAndSwap(ctx, accountStorageKey(id), data, nil); err != nil {
		return mapConflict("delete account", err)
	}
	return nil
}

func decodeAccount(data []byte) (accountRecord, error) {
	var stored accountRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return accountRecord{}, fmt.Errorf("%w: %s", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion {
		return accountRecord{}, fmt.Errorf(
			"%w: unsupported schema version %d",
			ErrCorruptRecord,
			stored.SchemaVersion,
		)
	}
	if stored.Revision == 0 {
		return accountRecord{}, fmt.Errorf("%w: account revision is zero", ErrCorruptRecord)
	}
	if err := stored.Account.Validate(); err != nil {
		return accountRecord{}, fmt.Errorf("%w: %s", ErrCorruptRecord, err)
	}
	return stored, nil
}

func accountStorageKey(id string) string { return accountKeyPrefix + encodePart(id) }

func encodePart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func cloneAccount(value Account) Account {
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
