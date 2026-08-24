package authmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/storage"
)

const (
	// StorageSchemaVersion identifies the only authentication-mode schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the authentication-mode v1 namespace.
	StoragePrefix = "authmode:v1:"
	// StorageKey holds the one stored setting. The mode is deployment-wide, so
	// there is one record and not a keyed collection.
	StorageKey = StoragePrefix + "current"
)

var (
	// ErrRepositoryRequired reports an absent authentication-mode storage adapter.
	ErrRepositoryRequired = errors.New("authentication mode storage is required")
	// ErrNotFound reports that no mode has been stored.
	ErrNotFound = errors.New("stored authentication mode not found")
	// ErrConflict reports an authentication-mode revision conflict.
	ErrConflict = errors.New("authentication mode revision conflict")
	// ErrCorruptRecord reports invalid durable authentication-mode data.
	ErrCorruptRecord = errors.New("authentication mode record is invalid")
	// ErrInvalidMode reports a mode the gateway cannot run in.
	ErrInvalidMode = errors.New("authentication mode is invalid")
)

// Record is the versioned stored setting.
type Record struct {
	Revision uint64
	Setting  Setting
}

// Repository is the durable authentication-mode contract. A stored mode is
// what makes a console change outlive the process that accepted it.
type Repository interface {
	Get(context.Context) (Record, error)
	Put(context.Context, Setting, uint64) (Record, error)
}

type repository struct{ store storage.KVStore }

type storedSetting struct {
	SchemaVersion int     `json:"schema_version"`
	Revision      uint64  `json:"revision"`
	Setting       Setting `json:"setting"`
}

// Open returns a storage-backed authentication-mode repository.
func Open(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store}, nil
}

func (r *repository) Get(ctx context.Context) (Record, error) {
	data, err := r.store.Get(ctx, StorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("get authentication mode: %w", err)
	}
	stored, err := decode(data)
	if err != nil {
		return Record{}, err
	}
	return Record{Revision: stored.Revision, Setting: stored.Setting}, nil
}

// Put stores setting, replacing the record at expectedRevision. A zero
// expected revision writes the first record and refuses if one already exists,
// so two operators changing the mode at once cannot silently overwrite each
// other.
func (r *repository) Put(ctx context.Context, setting Setting, expectedRevision uint64) (Record, error) {
	if !setting.Mode.Valid() || setting.Mode == "" {
		return Record{}, fmt.Errorf("%w: %q", ErrInvalidMode, setting.Mode)
	}
	var currentData []byte
	if expectedRevision != 0 {
		data, err := r.store.Get(ctx, StorageKey)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return Record{}, ErrConflict
			}
			return Record{}, fmt.Errorf("get authentication mode for update: %w", err)
		}
		stored, err := decode(data)
		if err != nil {
			return Record{}, err
		}
		if stored.Revision != expectedRevision {
			return Record{}, ErrConflict
		}
		currentData = data
	}

	updated := storedSetting{
		SchemaVersion: StorageSchemaVersion,
		Revision:      expectedRevision + 1,
		Setting:       setting.Effective(),
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return Record{}, fmt.Errorf("encode authentication mode: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, StorageKey, currentData, data); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return Record{}, ErrConflict
		}
		return Record{}, fmt.Errorf("store authentication mode: %w", err)
	}
	return Record{Revision: updated.Revision, Setting: updated.Setting}, nil
}

func decode(data []byte) (storedSetting, error) {
	var stored storedSetting
	if err := json.Unmarshal(data, &stored); err != nil {
		return storedSetting{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion || stored.Revision == 0 {
		return storedSetting{}, fmt.Errorf("%w: unsupported schema or revision", ErrCorruptRecord)
	}
	// A stored mode the gateway does not recognize is refused rather than
	// resolved to a default. Reading it as "required" would be safe and
	// silent, and an operator whose stored mode stopped applying deserves to
	// be told rather than to discover it from a 401.
	if !stored.Setting.Mode.Valid() || stored.Setting.Mode == "" {
		return storedSetting{}, fmt.Errorf("%w: mode %q", ErrCorruptRecord, stored.Setting.Mode)
	}
	return stored, nil
}
