package files

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
	// StorageSchemaVersion identifies the only file record schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the file record v1 namespace.
	StoragePrefix = "files:v1:account:"

	defaultListLimit = 1000
)

var (
	// ErrRepositoryRequired reports an absent file record storage adapter.
	ErrRepositoryRequired = errors.New("files: record storage is required")
	// ErrFileNotFound reports a file this account cannot see.
	//
	// A file another account owns produces this error rather than a refusal. A
	// refusal would confirm that the identifier exists, and an identifier is
	// the only thing a caller needs to guess.
	ErrFileNotFound = errors.New("files: file not found")
	// ErrFileExists reports an identifier already in use.
	ErrFileExists = errors.New("files: file already exists")
	// ErrCorruptRecord reports durable file data this package cannot read.
	ErrCorruptRecord = errors.New("files: file record is invalid")
)

// Repository is the durable file record contract. It stores records and never
// bytes, which is why every method here is cheap and none of them streams.
type Repository interface {
	Create(context.Context, File) error
	Get(context.Context, string, string) (File, error)
	List(context.Context, string, int) ([]File, error)
	Replace(context.Context, File) error
	Delete(context.Context, string, string) error
	Scan(context.Context, int) ([]File, error)
}

type repository struct{ store storage.KVStore }

// fileRecord is the durable form. It carries the blob key that File keeps
// unexported, because the record store is the one place the key belongs.
type fileRecord struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	Account       string    `json:"account"`
	Filename      string    `json:"filename"`
	Purpose       Purpose   `json:"purpose"`
	Bytes         int64     `json:"bytes"`
	State         FileState `json:"state"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	BlobKey       string    `json:"blob_key"`
}

// OpenRepository returns a storage-backed file record repository.
func OpenRepository(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store}, nil
}

func (r *repository) Create(ctx context.Context, file File) error {
	data, err := encodeFile(file)
	if err != nil {
		return err
	}
	if err := r.store.CompareAndSwap(ctx, storageKey(file.Account, file.ID), nil, data); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return ErrFileExists
		}
		return fmt.Errorf("files: create record: %w", err)
	}
	return nil
}

func (r *repository) Get(ctx context.Context, account, id string) (File, error) {
	if strings.TrimSpace(account) == "" || strings.TrimSpace(id) == "" {
		return File{}, ErrFileNotFound
	}
	data, err := r.store.Get(ctx, storageKey(account, id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return File{}, ErrFileNotFound
		}
		return File{}, fmt.Errorf("files: read record: %w", err)
	}
	return decodeFile(data)
}

func (r *repository) List(ctx context.Context, account string, limit int) ([]File, error) {
	if strings.TrimSpace(account) == "" {
		return nil, nil
	}
	return r.readPrefix(ctx, accountPrefix(account), limit)
}

// Scan reads every account's records. The sweep is the only caller, because it
// answers a question no account asks: which records did a stopped process leave
// behind.
func (r *repository) Scan(ctx context.Context, limit int) ([]File, error) {
	return r.readPrefix(ctx, StoragePrefix, limit)
}

func (r *repository) readPrefix(ctx context.Context, prefix string, limit int) ([]File, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	keys, err := r.store.ScanWithPrefix(ctx, prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("files: list records: %w", err)
	}
	sort.Strings(keys)
	records := make([]File, 0, len(keys))
	for _, key := range keys {
		data, err := r.store.Get(ctx, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				// A record deleted between the scan and the read is not an
				// error. A sweep and a delete run at the same time.
				continue
			}
			return nil, fmt.Errorf("files: read listed record: %w", err)
		}
		file, err := decodeFile(data)
		if err != nil {
			return nil, err
		}
		records = append(records, file)
	}
	return records, nil
}

// Replace writes a record that already exists. It is how a pending record
// becomes ready and how a ready record starts deleting.
func (r *repository) Replace(ctx context.Context, file File) error {
	data, err := encodeFile(file)
	if err != nil {
		return err
	}
	key := storageKey(file.Account, file.ID)
	current, err := r.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrFileNotFound
		}
		return fmt.Errorf("files: read record for replace: %w", err)
	}
	if err := r.store.CompareAndSwap(ctx, key, current, data); err != nil {
		return fmt.Errorf("files: replace record: %w", err)
	}
	return nil
}

func (r *repository) Delete(ctx context.Context, account, id string) error {
	if err := r.store.Delete(ctx, storageKey(account, id)); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// A repeated delete is safe, so a sweep that runs twice over the
			// same record does not fail the second time.
			return nil
		}
		return fmt.Errorf("files: delete record: %w", err)
	}
	return nil
}

// storageKey puts the account above the identifier, so a read for another
// account misses by construction rather than by a check a later change could
// forget.
func storageKey(account, id string) string {
	return accountPrefix(account) + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func accountPrefix(account string) string {
	return StoragePrefix + base64.RawURLEncoding.EncodeToString([]byte(account)) + ":id:"
}

func encodeFile(file File) ([]byte, error) {
	if err := file.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(fileRecord{
		SchemaVersion: StorageSchemaVersion,
		ID:            file.ID,
		Account:       file.Account,
		Filename:      file.Filename,
		Purpose:       file.Purpose,
		Bytes:         file.Bytes,
		State:         file.State,
		CreatedAt:     file.CreatedAt,
		ExpiresAt:     file.ExpiresAt,
		BlobKey:       file.blobKey,
	})
	if err != nil {
		return nil, fmt.Errorf("files: encode record: %w", err)
	}
	return data, nil
}

func decodeFile(data []byte) (File, error) {
	var stored fileRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return File{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion {
		return File{}, fmt.Errorf("%w: unsupported schema %d", ErrCorruptRecord, stored.SchemaVersion)
	}
	file := File{
		ID:        stored.ID,
		Account:   stored.Account,
		Filename:  stored.Filename,
		Purpose:   stored.Purpose,
		Bytes:     stored.Bytes,
		State:     stored.State,
		CreatedAt: stored.CreatedAt,
		ExpiresAt: stored.ExpiresAt,
		blobKey:   stored.BlobKey,
	}
	if err := file.Validate(); err != nil {
		return File{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return file, nil
}
