package jobs

import (
	"bytes"
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
	// BatchStorageSchemaVersion identifies the schema with batch-scoped authorization evidence.
	BatchStorageSchemaVersion = 5
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
	ClaimLine(context.Context, string, string, int, string) (BatchLine, error)
	ReadLine(context.Context, string, string, int) (BatchLine, error)
	BindLineOutput(context.Context, BatchLine, ResultFile) (BatchLine, error)
	RecordLineResult(context.Context, BatchLine, string, int64, bool) (BatchLine, error)
	ConfirmLineResult(context.Context, BatchLine) (BatchLine, error)
	Create(context.Context, Batch) error
	// CreateClaimed atomically stores the batch and its prepared claim attachment.
	CreateClaimed(context.Context, Batch, storage.CompareAndSwapMutation) error
	Get(context.Context, string, string) (Batch, error)
	List(context.Context, string, int) ([]Batch, error)
	RecoveryPage(context.Context, string) (RecoveryPage[Batch], error)
	Replace(ctx context.Context, expected, next Batch) error
}

type batchRepository struct{ store storage.KVStore }

// batchRecord is the durable form.
type batchRecord struct {
	Authorization    []byte    `json:"authorization,omitempty"`
	StoredBytesBound int64     `json:"stored_bytes_bound,omitzero"`
	ResultsReleased  bool      `json:"results_released,omitzero"`
	ClaimedLines     int       `json:"claimed_lines,omitzero"`
	SlotID           string    `json:"slot_id,omitempty"`
	SlotReleased     bool      `json:"slot_released,omitzero"`
	RunFinished      bool      `json:"run_finished,omitzero"`
	SchemaVersion    int       `json:"schema_version"`
	ID               string    `json:"id"`
	Account          string    `json:"account"`
	KeyID            string    `json:"key_id,omitempty"`
	Endpoint         string    `json:"endpoint"`
	InputFileID      string    `json:"input_file_id"`
	OutputFileID     string    `json:"output_file_id,omitempty"`
	ErrorFileID      string    `json:"error_file_id,omitempty"`
	State            JobState  `json:"state"`
	Reason           string    `json:"reason,omitempty"`
	TotalLines       int       `json:"total_lines,omitempty"`
	CompletedLines   int       `json:"completed_lines,omitempty"`
	FailedLines      int       `json:"failed_lines,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	TerminalAt       time.Time `json:"terminal_at,omitempty"`
}

// OpenBatchRepository returns a storage-backed batch record repository.
func OpenBatchRepository(store storage.KVStore) (BatchRepository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &batchRepository{store: store}, nil
}

func (r *batchRepository) Create(ctx context.Context, batch Batch) error {
	if batch.SlotID != "" {
		return ErrClaimAttachmentRequired
	}
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
func (r *batchRepository) Replace(ctx context.Context, expected, batch Batch) error {
	if !bytes.Equal(expected.Authorization, batch.Authorization) {
		return ErrInvalidBatch
	}
	if expected.TotalLines > 0 && expected.TotalLines != batch.TotalLines {
		return ErrInvalidBatch
	}
	if expected.StoredBytesBound != batch.StoredBytesBound || expected.ResultsReleased && !batch.ResultsReleased {
		return ErrInvalidBatch
	}
	if expected.RunFinished && (expected.OutputFileID != batch.OutputFileID || expected.ErrorFileID != batch.ErrorFileID || expected.CompletedLines != batch.CompletedLines || expected.FailedLines != batch.FailedLines) {
		return ErrInvalidBatch
	}
	if expected.Account != batch.Account || expected.ID != batch.ID || expected.SlotID != batch.SlotID || expected.ClaimedLines != batch.ClaimedLines || expected.KeyID != batch.KeyID || expected.InputFileID != batch.InputFileID || expected.Endpoint != batch.Endpoint || !expected.CreatedAt.Equal(batch.CreatedAt) {
		return ErrInvalidBatch
	}
	previous, err := encodeBatch(expected)
	if err != nil {
		return err
	}
	data, err := encodeBatch(batch)
	if err != nil {
		return err
	}
	key := batchStorageKey(batch.Account, batch.ID)
	current, err := r.store.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return ErrBatchNotFound
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(current, previous) {
		return storage.ErrConflict
	}
	if expected.State != batch.State && !CanTransition(expected.State, batch.State) {
		return ErrIllegalTransition
	}
	if expected.RunFinished && !batch.RunFinished || expected.SlotReleased && !batch.SlotReleased {
		return ErrInvalidBatch
	}
	return r.store.CompareAndSwap(ctx, key, previous, data)
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
		Authorization:    batch.Authorization,
		StoredBytesBound: batch.StoredBytesBound, ResultsReleased: batch.ResultsReleased,
		ClaimedLines:   batch.ClaimedLines,
		SlotID:         batch.SlotID,
		SlotReleased:   batch.SlotReleased,
		RunFinished:    batch.RunFinished,
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
		Authorization:    stored.Authorization,
		StoredBytesBound: stored.StoredBytesBound, ResultsReleased: stored.ResultsReleased,
		ClaimedLines:   stored.ClaimedLines,
		SlotID:         stored.SlotID,
		SlotReleased:   stored.SlotReleased,
		RunFinished:    stored.RunFinished,
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
