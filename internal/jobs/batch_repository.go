package jobs

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
	// BatchStorageSchemaVersion identifies the only batch record schema.
	BatchStorageSchemaVersion = 1
	// BatchStoragePrefix is the batch record v1 namespace.
	BatchStoragePrefix = "batches:v1:account:"
)

var (
	// ErrBatchNotFound reports a batch this account cannot see. A batch
	// another account owns produces this error rather than a refusal, for the
	// reason ErrJobNotFound states.
	ErrBatchNotFound = errors.New("jobs: batch not found")
	// ErrBatchExists reports a batch identifier already in use.
	ErrBatchExists = errors.New("jobs: batch already exists")
	// ErrCorruptBatchRecord reports durable batch data this package cannot read.
	ErrCorruptBatchRecord = errors.New("jobs: batch record is invalid")
)

// BatchRepository is the durable batch record contract. Every method takes the
// account, so a store cannot answer with a record its caller does not own.
// Replace carries the whole record because a state change is never the only
// change: a terminal move stamps a time and attaches the result files.
type BatchRepository interface {
	Create(context.Context, Batch) error
	Get(context.Context, string, string) (Batch, error)
	List(context.Context, string, int) ([]Batch, error)
	Replace(context.Context, Batch) error
}

type batchRepository struct{ store storage.KVStore }

// batchRecord is the durable form.
type batchRecord struct {
	SchemaVersion  int       `json:"schema_version"`
	ID             string    `json:"id"`
	Account        string    `json:"account"`
	KeyID          string    `json:"key_id,omitempty"`
	Endpoint       string    `json:"endpoint"`
	InputFileID    string    `json:"input_file_id"`
	OutputFileID   string    `json:"output_file_id,omitempty"`
	ErrorFileID    string    `json:"error_file_id,omitempty"`
	State          JobState  `json:"state"`
	Reason         string    `json:"reason,omitempty"`
	TotalLines     int       `json:"total_lines,omitempty"`
	CompletedLines int       `json:"completed_lines,omitempty"`
	FailedLines    int       `json:"failed_lines,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	TerminalAt     time.Time `json:"terminal_at,omitempty"`
}

// OpenBatchRepository returns a storage-backed batch record repository.
func OpenBatchRepository(store storage.KVStore) (BatchRepository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &batchRepository{store: store}, nil
}

func (r *batchRepository) Create(ctx context.Context, batch Batch) error {
	data, err := encodeBatch(batch)
	if err != nil {
		return err
	}
	if err := r.store.CompareAndSwap(ctx, batchStorageKey(batch.Account, batch.ID), nil, data); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return ErrBatchExists
		}
		return fmt.Errorf("jobs: create batch record: %w", err)
	}
	return nil
}

func (r *batchRepository) Get(ctx context.Context, account, id string) (Batch, error) {
	if strings.TrimSpace(account) == "" || strings.TrimSpace(id) == "" {
		return Batch{}, ErrBatchNotFound
	}
	data, err := r.store.Get(ctx, batchStorageKey(account, id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Batch{}, ErrBatchNotFound
		}
		return Batch{}, fmt.Errorf("jobs: read batch record: %w", err)
	}
	return decodeBatch(data)
}

// List answers newest first, the way the job listing does and for the same
// reason: a caller polling a batch it just submitted looks at the top.
func (r *batchRepository) List(ctx context.Context, account string, limit int) ([]Batch, error) {
	if strings.TrimSpace(account) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	records, err := readRecordsUnder(ctx, r.store, batchAccountPrefix(account), limit, decodeBatch, "batch")
	if err != nil {
		return nil, err
	}
	sortBatchesNewestFirst(records)
	return records, nil
}

func sortBatchesNewestFirst(records []Batch) {
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.After(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
}

// Replace writes a record that already exists, and it is the point at which a
// batch state change meets the one transition table.
func (r *batchRepository) Replace(ctx context.Context, batch Batch) error {
	data, err := encodeBatch(batch)
	if err != nil {
		return err
	}
	return replaceRecord(ctx, r.store, batchStorageKey(batch.Account, batch.ID), data,
		func(current []byte) (JobState, error) {
			stored, err := decodeBatch(current)
			return stored.State, err
		}, batch.State, ErrBatchNotFound, "batch")
}

// batchStorageKey puts the account above the identifier, so a read for
// another account misses by construction.
func batchStorageKey(account, id string) string {
	return batchAccountPrefix(account) + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func batchAccountPrefix(account string) string {
	return BatchStoragePrefix + base64.RawURLEncoding.EncodeToString([]byte(account)) + ":id:"
}

func encodeBatch(batch Batch) ([]byte, error) {
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(batchRecord{
		SchemaVersion:  BatchStorageSchemaVersion,
		ID:             batch.ID,
		Account:        batch.Account,
		KeyID:          batch.KeyID,
		Endpoint:       batch.Endpoint,
		InputFileID:    batch.InputFileID,
		OutputFileID:   batch.OutputFileID,
		ErrorFileID:    batch.ErrorFileID,
		State:          batch.State,
		Reason:         batch.Reason,
		TotalLines:     batch.TotalLines,
		CompletedLines: batch.CompletedLines,
		FailedLines:    batch.FailedLines,
		CreatedAt:      batch.CreatedAt,
		TerminalAt:     batch.TerminalAt,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: encode batch record: %w", err)
	}
	return data, nil
}

func decodeBatch(data []byte) (Batch, error) {
	var stored batchRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return Batch{}, fmt.Errorf("%w: decode: %v", ErrCorruptBatchRecord, err)
	}
	if stored.SchemaVersion != BatchStorageSchemaVersion {
		return Batch{}, fmt.Errorf("%w: unsupported schema %d", ErrCorruptBatchRecord, stored.SchemaVersion)
	}
	batch := Batch{
		ID:             stored.ID,
		Account:        stored.Account,
		KeyID:          stored.KeyID,
		Endpoint:       stored.Endpoint,
		InputFileID:    stored.InputFileID,
		OutputFileID:   stored.OutputFileID,
		ErrorFileID:    stored.ErrorFileID,
		State:          stored.State,
		Reason:         stored.Reason,
		TotalLines:     stored.TotalLines,
		CompletedLines: stored.CompletedLines,
		FailedLines:    stored.FailedLines,
		CreatedAt:      stored.CreatedAt,
		TerminalAt:     stored.TerminalAt,
	}
	if err := batch.Validate(); err != nil {
		return Batch{}, fmt.Errorf("%w: %v", ErrCorruptBatchRecord, err)
	}
	return batch, nil
}
