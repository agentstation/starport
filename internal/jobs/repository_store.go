package jobs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
)

const (
	// StorageSchemaVersion includes durable correction heads and report progress.
	StorageSchemaVersion = 6
	// StoragePrefix is the job record v1 namespace.
	StoragePrefix = "jobs:v1:account:"

	defaultListLimit = 1000
)

type repository struct{ store storage.KVStore }

// jobRecord is the durable form. It carries the provider job identifier that
// Job keeps unexported, because the record store is the one place it belongs.
type jobRecord struct {
	CorrectionHead         *CorrectionIntent       `json:"correction_head,omitempty"`
	CorrectionApplied      *CorrectionIntent       `json:"correction_applied,omitempty"`
	CorrectionReported     string                  `json:"correction_reported,omitempty"`
	AssetPending           bool                    `json:"asset_pending,omitempty"`
	AssetDigest            string                  `json:"asset_digest,omitempty"`
	AdminDecision          *ReconciliationDecision `json:"admin_decision,omitempty"`
	LateProviderEvidence   *LateProviderEvidence   `json:"late_provider_evidence,omitempty"`
	NativeAssetDigest      string                  `json:"native_asset_digest,omitempty"`
	NativeAssetContentType string                  `json:"native_asset_content_type,omitempty"`
	AssetRecoveryStatus    string                  `json:"asset_recovery_status,omitempty"`
	NativeAssetBound       int64                   `json:"native_asset_bound,omitzero"`
	NativeRetention        time.Duration           `json:"native_retention,omitzero"`
	Native                 bool                    `json:"native,omitzero"`
	Valuation              *reservation.Valuation  `json:"valuation,omitempty"`
	Measurement            *reservation.Evidence   `json:"measurement,omitempty"`
	NativeReceiptKey       string                  `json:"native_receipt_key,omitempty"`
	NativeAssetKey         string                  `json:"native_asset_key,omitempty"`
	SlotID                 string                  `json:"slot_id,omitempty"`
	SlotReleased           bool                    `json:"slot_released,omitzero"`
	SubmissionPending      bool                    `json:"submission_pending,omitzero"`
	CatalogGeneration      string                  `json:"catalog_generation,omitempty"`
	ReservationID          string                  `json:"reservation_id,omitempty"`
	SchemaVersion          int                     `json:"schema_version"`
	ID                     string                  `json:"id"`
	Account                string                  `json:"account"`
	KeyID                  string                  `json:"key_id,omitempty"`
	Model                  string                  `json:"model"`
	Operation              routing.Operation       `json:"operation"`
	Provider               string                  `json:"provider"`
	State                  JobState                `json:"state"`
	Reason                 string                  `json:"reason,omitempty"`
	CreatedAt              time.Time               `json:"created_at"`
	TerminalAt             time.Time               `json:"terminal_at,omitempty"`
	ProviderJobID          string                  `json:"provider_job_id,omitempty"`

	AssetKey         string    `json:"asset_key,omitempty"`
	AssetBytes       int64     `json:"asset_bytes,omitempty"`
	AssetContentType string    `json:"asset_content_type,omitempty"`
	AssetExpiresAt   time.Time `json:"asset_expires_at,omitempty"`
	AssetExpiredAt   time.Time `json:"asset_expired_at,omitempty"`

	AccountedAt             time.Time `json:"accounted_at,omitempty"`
	ReportingExpiredAt      time.Time `json:"reporting_expired_at,omitempty"`
	NotificationAttemptedAt time.Time `json:"notification_attempted_at,omitempty"`
}

// OpenRepository returns a storage-backed job record repository.
func OpenRepository(store storage.KVStore) (Repository, error) {
	if store == nil {
		return nil, ErrRepositoryRequired
	}
	return &repository{store: store}, nil
}

func (r *repository) Create(ctx context.Context, job Job) error {
	if job.correctionHead != nil {
		return ErrInvalidJob
	}
	if job.SlotID != "" {
		return ErrClaimAttachmentRequired
	}
	data, err := encodeJob(job)
	if err != nil {
		return err
	}
	if err := r.store.CompareAndSwap(ctx, storageKey(job.Account, job.ID), nil, data); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return ErrJobExists
		}
		return fmt.Errorf("jobs: create record: %w", err)
	}
	return nil
}

func (r *repository) Get(ctx context.Context, account, id string) (Job, error) {
	if strings.TrimSpace(account) == "" || strings.TrimSpace(id) == "" {
		return Job{}, ErrJobNotFound
	}
	data, err := r.store.Get(ctx, storageKey(account, id))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Job{}, ErrJobNotFound
		}
		return Job{}, fmt.Errorf("jobs: read record: %w", err)
	}
	return decodeJob(data)
}

// List returns the newest jobs first so callers can find recent submissions.
// Storage key order does not define this order.
func (r *repository) List(ctx context.Context, account string, limit int) ([]Job, error) {
	if strings.TrimSpace(account) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	records, err := readRecordsUnder(ctx, r.store, accountPrefix(account), limit, decodeJob, "job")
	if err != nil {
		return nil, err
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

// Scan answers a bounded set of deployment records, sorted newest first.
//
// It reads across accounts because the sweep that reclaims expired asset storage
// is a deployment-wide pass. Nothing on a request path calls it: a caller reads
// its own jobs through List, which cannot see another account's prefix.
func (r *repository) Scan(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	records, err := readRecordsUnder(ctx, r.store, StoragePrefix, limit, decodeJob, "job")
	if err != nil {
		return nil, err
	}
	sortNewestFirst(records)
	return records, nil
}

func (r *repository) Replace(ctx context.Context, expected, job Job) error {
	mutation, err := r.Replacement(ctx, expected, job)
	if err != nil {
		return err
	}
	if err := r.store.CompareAndSwap(ctx, mutation.Key, mutation.ExpectedValue, mutation.NewValue); err != nil {
		return fmt.Errorf("jobs: replace record: %w", err)
	}
	return nil
}

// Replacement validates a job change for an atomic write with required settlement.
// The caller must commit it against the same storage authority as this repository.
func (r *repository) Replacement(ctx context.Context, expected, job Job) (storage.CompareAndSwapMutation, error) {
	if !unchangedCorrections(expected, job) {
		return storage.CompareAndSwapMutation{}, ErrInvalidJob
	}
	return r.replacement(ctx, expected, job)
}

func (r *repository) replacement(ctx context.Context, expected, job Job) (storage.CompareAndSwapMutation, error) {
	if err := validateRetainedJobEvidence(expected, job); err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if expected.nativeAssetBound != job.nativeAssetBound || expected.nativeRetention != job.nativeRetention || expected.Native != job.Native || expected.nativeReceiptKey != job.nativeReceiptKey || expected.nativeAssetKey != job.nativeAssetKey || !reflect.DeepEqual(expected.Valuation, job.Valuation) || (expected.Measurement != nil && !reflect.DeepEqual(expected.Measurement, job.Measurement)) {
		return storage.CompareAndSwapMutation{}, ErrInvalidJob
	}
	if expected.nativeAssetDigest != "" && (expected.nativeAssetDigest != job.nativeAssetDigest || expected.nativeAssetContentType != job.nativeAssetContentType) {
		return storage.CompareAndSwapMutation{}, ErrInvalidJob
	}
	if expected.SlotReleased && !job.SlotReleased {
		return storage.CompareAndSwapMutation{}, ErrInvalidJob
	}
	if !sameJobIdentity(expected, job) {
		return storage.CompareAndSwapMutation{}, ErrInvalidJob
	}
	if expected.SlotID != job.SlotID || expected.CatalogGeneration != job.CatalogGeneration || expected.ReservationID != job.ReservationID {
		return storage.CompareAndSwapMutation{}, ErrInvalidJob
	}
	previous, err := encodeJob(expected)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	data, err := encodeJob(job)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	key := storageKey(job.Account, job.ID)
	current, err := r.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.CompareAndSwapMutation{}, ErrJobNotFound
		}
		return storage.CompareAndSwapMutation{}, fmt.Errorf("jobs: read record for replace: %w", err)
	}
	if !bytes.Equal(current, previous) {
		return storage.CompareAndSwapMutation{}, storage.ErrConflict
	}
	if expected.State != job.State && !CanTransition(expected.State, job.State) {
		return storage.CompareAndSwapMutation{}, fmt.Errorf("%w: %q to %q", ErrIllegalTransition, expected.State, job.State)
	}
	return storage.CompareAndSwapMutation{Key: key, ExpectedValue: previous, NewValue: data}, nil
}

func (r *repository) Delete(ctx context.Context, account, id string) error {
	key := storageKey(account, id)
	data, err := r.store.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	job, err := decodeJob(data)
	if err != nil {
		return err
	}
	if job.adminDecision != nil {
		return ErrAuditRetention
	}
	if job.AssetKey != "" && job.AssetExpiredAt.IsZero() {
		return ErrAssetRetirementRequired
	}
	return r.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{{Key: key, ExpectedValue: data}})
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

func encodeJob(job Job) ([]byte, error) {
	if err := job.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(jobRecord{
		CorrectionHead: job.correctionHead, CorrectionApplied: job.correctionApplied, CorrectionReported: job.correctionReported,
		AssetPending: job.assetPending, AssetDigest: job.assetDigest,
		AdminDecision: job.adminDecision, LateProviderEvidence: job.lateProviderEvidence,
		NativeAssetDigest: job.nativeAssetDigest, NativeAssetContentType: job.nativeAssetContentType, AssetRecoveryStatus: job.assetRecoveryStatus,
		NativeAssetBound: job.nativeAssetBound, NativeRetention: job.nativeRetention,
		Native: job.Native, Valuation: job.Valuation, Measurement: job.Measurement, NativeReceiptKey: job.nativeReceiptKey, NativeAssetKey: job.nativeAssetKey,
		SlotID:            job.SlotID,
		SlotReleased:      job.SlotReleased,
		SchemaVersion:     StorageSchemaVersion,
		SubmissionPending: job.SubmissionPending,
		CatalogGeneration: job.CatalogGeneration,
		ReservationID:     job.ReservationID,
		ID:                job.ID,
		Account:           job.Account,
		KeyID:             job.KeyID,
		Model:             job.Model,
		Operation:         job.Operation,
		Provider:          job.Provider,
		State:             job.State,
		Reason:            job.Reason,
		CreatedAt:         job.CreatedAt,
		TerminalAt:        job.TerminalAt,
		ProviderJobID:     job.providerJobID,

		AssetKey:         job.AssetKey,
		AssetBytes:       job.AssetBytes,
		AssetContentType: job.AssetContentType,
		AssetExpiresAt:   job.AssetExpiresAt,
		AssetExpiredAt:   job.AssetExpiredAt,

		AccountedAt:             job.AccountedAt,
		ReportingExpiredAt:      job.ReportingExpiredAt,
		NotificationAttemptedAt: job.NotificationAttemptedAt,
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
		correctionHead: stored.CorrectionHead, correctionApplied: stored.CorrectionApplied, correctionReported: stored.CorrectionReported,
		assetPending: stored.AssetPending, assetDigest: stored.AssetDigest,
		adminDecision: stored.AdminDecision, lateProviderEvidence: stored.LateProviderEvidence,
		nativeAssetDigest: stored.NativeAssetDigest, nativeAssetContentType: stored.NativeAssetContentType, assetRecoveryStatus: stored.AssetRecoveryStatus,
		nativeAssetBound: stored.NativeAssetBound, nativeRetention: stored.NativeRetention,
		Native: stored.Native, Valuation: stored.Valuation, Measurement: stored.Measurement, nativeReceiptKey: stored.NativeReceiptKey, nativeAssetKey: stored.NativeAssetKey,
		SlotID:            stored.SlotID,
		SlotReleased:      stored.SlotReleased,
		SubmissionPending: stored.SubmissionPending,
		CatalogGeneration: stored.CatalogGeneration,
		ReservationID:     stored.ReservationID,
		ID:                stored.ID,
		Account:           stored.Account,
		KeyID:             stored.KeyID,
		Model:             stored.Model,
		Operation:         stored.Operation,
		Provider:          stored.Provider,
		State:             stored.State,
		Reason:            stored.Reason,
		CreatedAt:         stored.CreatedAt,
		TerminalAt:        stored.TerminalAt,
		providerJobID:     stored.ProviderJobID,

		AssetKey:         stored.AssetKey,
		AssetBytes:       stored.AssetBytes,
		AssetContentType: stored.AssetContentType,
		AssetExpiresAt:   stored.AssetExpiresAt,
		AssetExpiredAt:   stored.AssetExpiredAt,

		AccountedAt:             stored.AccountedAt,
		ReportingExpiredAt:      stored.ReportingExpiredAt,
		NotificationAttemptedAt: stored.NotificationAttemptedAt,
	}
	if err := job.Validate(); err != nil {
		return Job{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	return job, nil
}

func validateRetainedJobEvidence(expected, job Job) error {
	if !expected.ReportingExpiredAt.IsZero() && !expected.ReportingExpiredAt.Equal(job.ReportingExpiredAt) {
		return ErrInvalidJob
	}
	if expected.assetDigest != "" && (expected.assetDigest != job.assetDigest || expected.AssetKey != job.AssetKey || expected.AssetContentType != job.AssetContentType || expected.AssetBytes != job.AssetBytes || !expected.AssetExpiresAt.Equal(job.AssetExpiresAt) || (!expected.assetPending && job.assetPending)) {
		return ErrInvalidJob
	}
	if !expected.AssetExpiredAt.IsZero() && !expected.AssetExpiredAt.Equal(job.AssetExpiredAt) {
		return ErrInvalidJob
	}
	if !immutableAdministrator(expected, job) {
		return ErrInvalidJob
	}
	return nil
}

func sameJobIdentity(expected, job Job) bool {
	return expected.Account == job.Account && expected.ID == job.ID && expected.KeyID == job.KeyID && expected.Provider == job.Provider && expected.Model == job.Model && expected.Operation == job.Operation && expected.CreatedAt.Equal(job.CreatedAt)
}
