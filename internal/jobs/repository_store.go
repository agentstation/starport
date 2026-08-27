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

	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
)

const (
	// StorageSchemaVersion identifies the only job record schema.
	StorageSchemaVersion = 1
	// StoragePrefix is the job record v1 namespace.
	StoragePrefix = "jobs:v1:tenant:"

	defaultListLimit = 1000
)

type repository struct{ store storage.KVStore }

// jobRecord is the durable form. It carries the provider job identifier that
// Job keeps unexported, because the record store is the one place it belongs.
type jobRecord struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	Tenant        string            `json:"tenant"`
	Model         string            `json:"model"`
	Operation     routing.Operation `json:"operation"`
	Provider      string            `json:"provider"`
	State         JobState          `json:"state"`
	Reason        string            `json:"reason,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	TerminalAt    time.Time         `json:"terminal_at,omitempty"`
	ProviderJobID string            `json:"provider_job_id,omitempty"`
}

// OpenRepository returns a storage-backed job record repository.
func OpenRepository(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store}, nil
}

func (r *repository) Create(ctx context.Context, job Job) error {
	data, err := encodeJob(job)
	if err != nil {
		return err
	}
	if err := r.store.CompareAndSwap(ctx, storageKey(job.Tenant, job.ID), nil, data); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return ErrJobExists
		}
		return fmt.Errorf("jobs: create record: %w", err)
	}
	return nil
}

func (r *repository) Get(ctx context.Context, tenant, id string) (Job, error) {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(id) == "" {
		return Job{}, ErrJobNotFound
	}
	data, err := r.store.Get(ctx, storageKey(tenant, id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Job{}, ErrJobNotFound
		}
		return Job{}, fmt.Errorf("jobs: read record: %w", err)
	}
	return decodeJob(data)
}

// List answers newest first. A caller polling a job it just submitted looks at
// the top of the page, and a storage layer that ordered by key would put it
// wherever its identifier happened to sort.
func (r *repository) List(ctx context.Context, tenant string, limit int) ([]Job, error) {
	if strings.TrimSpace(tenant) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	keys, err := r.store.ScanWithPrefix(ctx, tenantPrefix(tenant), limit)
	if err != nil {
		return nil, fmt.Errorf("jobs: list records: %w", err)
	}
	records := make([]Job, 0, len(keys))
	for _, key := range keys {
		data, err := r.store.Get(ctx, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				// A record deleted between the scan and the read is not an
				// error. A listing and a delete run at the same time.
				continue
			}
			return nil, fmt.Errorf("jobs: read listed record: %w", err)
		}
		job, err := decodeJob(data)
		if err != nil {
			return nil, err
		}
		records = append(records, job)
	}
	sortNewestFirst(records)
	return records, nil
}

// sortNewestFirst breaks a tie on the identifier. Two jobs submitted inside
// the same clock tick would otherwise swap places between two reads of the
// same unchanged data.
func sortNewestFirst(records []Job) {
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.After(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
}

// Replace writes a record that already exists, and it is the point at which a
// state change meets the transition table. A caller that assembled an illegal
// record fails here rather than in storage, so no store has to know which
// state changes this package allows.
func (r *repository) Replace(ctx context.Context, job Job) error {
	data, err := encodeJob(job)
	if err != nil {
		return err
	}
	key := storageKey(job.Tenant, job.ID)
	current, err := r.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrJobNotFound
		}
		return fmt.Errorf("jobs: read record for replace: %w", err)
	}
	stored, err := decodeJob(current)
	if err != nil {
		return err
	}
	if stored.State != job.State && !CanTransition(stored.State, job.State) {
		return fmt.Errorf("%w: %q to %q", ErrIllegalTransition, stored.State, job.State)
	}
	if err := r.store.CompareAndSwap(ctx, key, current, data); err != nil {
		return fmt.Errorf("jobs: replace record: %w", err)
	}
	return nil
}

func (r *repository) Delete(ctx context.Context, tenant, id string) error {
	if err := r.store.Delete(ctx, storageKey(tenant, id)); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// A repeated delete is safe, so a sweep that runs twice over the
			// same record does not fail the second time.
			return nil
		}
		return fmt.Errorf("jobs: delete record: %w", err)
	}
	return nil
}

// storageKey puts the tenant above the identifier, so a read for another
// tenant misses by construction rather than by a check a later change could
// forget.
func storageKey(tenant, id string) string {
	return tenantPrefix(tenant) + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func tenantPrefix(tenant string) string {
	return StoragePrefix + base64.RawURLEncoding.EncodeToString([]byte(tenant)) + ":id:"
}

func encodeJob(job Job) ([]byte, error) {
	if err := job.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(jobRecord{
		SchemaVersion: StorageSchemaVersion,
		ID:            job.ID,
		Tenant:        job.Tenant,
		Model:         job.Model,
		Operation:     job.Operation,
		Provider:      job.Provider,
		State:         job.State,
		Reason:        job.Reason,
		CreatedAt:     job.CreatedAt,
		TerminalAt:    job.TerminalAt,
		ProviderJobID: job.providerJobID,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: encode record: %w", err)
	}
	return data, nil
}

func decodeJob(data []byte) (Job, error) {
	var stored jobRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return Job{}, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != StorageSchemaVersion {
		return Job{}, fmt.Errorf("%w: unsupported schema %d", ErrCorruptRecord, stored.SchemaVersion)
	}
	job := Job{
		ID:            stored.ID,
		Tenant:        stored.Tenant,
		Model:         stored.Model,
		Operation:     stored.Operation,
		Provider:      stored.Provider,
		State:         stored.State,
		Reason:        stored.Reason,
		CreatedAt:     stored.CreatedAt,
		TerminalAt:    stored.TerminalAt,
		providerJobID: stored.ProviderJobID,
	}
	if err := job.Validate(); err != nil {
		return Job{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return job, nil
}
