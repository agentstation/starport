package presets

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
	// StorageSchemaVersion identifies the only preset schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the preset v1 namespace.
	StoragePrefix    = "presets:v1:name:"
	defaultListLimit = 1000
)

var (
	// ErrRepositoryRequired reports an absent preset storage adapter.
	ErrRepositoryRequired = errors.New("preset storage is required")
	// ErrNotFound reports an absent preset.
	ErrNotFound = errors.New("preset not found")
	// ErrConflict reports a preset revision conflict.
	ErrConflict = errors.New("preset revision conflict")
	// ErrCorruptRecord reports invalid durable preset data.
	ErrCorruptRecord = errors.New("preset record is invalid")
	// ErrNameImmutable reports an attempted preset-name change.
	ErrNameImmutable = errors.New("preset name is immutable")
)

// Record is one versioned preset repository value.
type Record struct {
	Revision uint64
	Preset   Preset
}

// Repository is the durable preset contract.
type Repository interface {
	Create(context.Context, Preset) (Record, error)
	Get(context.Context, string) (Record, error)
	List(context.Context, int) ([]Record, error)
	Update(context.Context, Preset, uint64) (Record, error)
	Delete(context.Context, string, uint64) error
}

type repository struct{ store storage.KVStore }

type presetRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	Preset        Preset `json:"preset"`
}

// Open returns a storage-backed preset repository.
func Open(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store}, nil
}

func (r *repository) Create(ctx context.Context, preset Preset) (Record, error) {
	if err := preset.Validate(); err != nil {
		return Record{}, err
	}
	stored := presetRecord{SchemaVersion: StorageSchemaVersion, Revision: 1, Preset: clonePreset(preset)}
	data, err := json.Marshal(stored)
	if err != nil {
		return Record{}, fmt.Errorf("encode preset: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, storageKey(preset.Name), nil, data); err != nil {
		return Record{}, mapConflict("create preset", err)
	}
	return recordFromPreset(stored), nil
}

func (r *repository) Get(ctx context.Context, name string) (Record, error) {
	if strings.TrimSpace(name) == "" {
		return Record{}, fmt.Errorf("invalid preset name")
	}
	data, err := r.store.Get(ctx, storageKey(name))
	if err != nil {
		return Record{}, mapReadError("get preset", err)
	}
	stored, err := decodePreset(data)
	if err != nil {
		return Record{}, err
	}
	if stored.Preset.Name != name {
		return Record{}, fmt.Errorf("%w: name does not match key", ErrCorruptRecord)
	}
	return recordFromPreset(stored), nil
}

func (r *repository) List(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	keys, err := r.store.ScanWithPrefix(ctx, StoragePrefix, limit)
	if err != nil {
		return nil, fmt.Errorf("list presets: %w", err)
	}
	sort.Strings(keys)
	records := make([]Record, 0, len(keys))
	for _, key := range keys {
		data, err := r.store.Get(ctx, key)
		if err != nil {
			return nil, mapReadError("read listed preset", err)
		}
		stored, err := decodePreset(data)
		if err != nil {
			return nil, err
		}
		records = append(records, recordFromPreset(stored))
	}
	return records, nil
}

func (r *repository) Update(ctx context.Context, preset Preset, expectedRevision uint64) (Record, error) {
	if err := preset.Validate(); err != nil {
		return Record{}, err
	}
	currentData, err := r.store.Get(ctx, storageKey(preset.Name))
	if err != nil {
		return Record{}, mapReadError("get preset for update", err)
	}
	current, err := decodePreset(currentData)
	if err != nil {
		return Record{}, err
	}
	if current.Revision != expectedRevision {
		return Record{}, ErrConflict
	}
	if current.Preset.Name != preset.Name {
		return Record{}, ErrNameImmutable
	}
	updated := presetRecord{SchemaVersion: StorageSchemaVersion, Revision: current.Revision + 1, Preset: clonePreset(preset)}
	updatedData, err := json.Marshal(updated)
	if err != nil {
		return Record{}, fmt.Errorf("encode preset update: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, storageKey(preset.Name), currentData, updatedData); err != nil {
		return Record{}, mapConflict("update preset", err)
	}
	return recordFromPreset(updated), nil
}

func (r *repository) Delete(ctx context.Context, name string, expectedRevision uint64) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("invalid preset name")
	}
	key := storageKey(name)
	data, err := r.store.Get(ctx, key)
	if err != nil {
		return mapReadError("get preset for delete", err)
	}
	stored, err := decodePreset(data)
	if err != nil {
		return err
	}
	if expectedRevision != 0 && stored.Revision != expectedRevision {
		return ErrConflict
	}
	if err := r.store.CompareAndSwap(ctx, key, data, nil); err != nil {
		return mapConflict("delete preset", err)
	}
	return nil
}

func decodePreset(data []byte) (presetRecord, error) {
	var stored presetRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return presetRecord{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion || stored.Revision == 0 {
		return presetRecord{}, fmt.Errorf("%w: unsupported schema or revision", ErrCorruptRecord)
	}
	if err := stored.Preset.Validate(); err != nil {
		return presetRecord{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return stored, nil
}

func storageKey(name string) string {
	return StoragePrefix + base64.RawURLEncoding.EncodeToString([]byte(name))
}

func recordFromPreset(stored presetRecord) Record {
	return Record{Revision: stored.Revision, Preset: clonePreset(stored.Preset)}
}

func clonePreset(preset Preset) Preset {
	if preset.Config != nil {
		config := make(map[string]any, len(preset.Config))
		for key, value := range preset.Config {
			config[key] = value
		}
		preset.Config = config
	}
	return preset
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
