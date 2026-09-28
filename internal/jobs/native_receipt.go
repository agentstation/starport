package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"reflect"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
)

// NativeResult retains a terminal provider response before job completion.
// State cannot derive from a local timeout or connection failure.
type NativeResult struct {
	State       JobState
	Reason      string
	RequestID   string
	Measurement *reservation.Evidence
	Asset       Asset
	// AssetURL is a reference only. A separate policy must authorize its download.
	AssetURL string
}

// NativeSubmissionRecorder supplies the service-owned asset bound to the router.
type NativeSubmissionRecorder interface {
	SubmissionRecorder
	AssetBound() int64
	ExecutionTimeout() time.Duration
}

func (r *submissionRecorder) ExecutionTimeout() time.Duration { return r.service.executionTimeout }

func (r *submissionRecorder) AssetBound() int64 { return r.service.maxAssetBytes }

// nativeReceipt binds one response to the dispatch that reserved its object keys.
type nativeReceipt struct {
	Version     int                   `json:"version"`
	JobID       string                `json:"job_id"`
	Account     string                `json:"account"`
	Provider    string                `json:"provider"`
	Model       string                `json:"model"`
	Generation  string                `json:"generation"`
	RequestID   string                `json:"request_id"`
	State       JobState              `json:"state"`
	Reason      string                `json:"reason"`
	RecordedAt  time.Time             `json:"recorded_at"`
	Measurement *reservation.Evidence `json:"measurement,omitempty"`
	ContentType string                `json:"content_type"`
	AssetBytes  int64                 `json:"asset_bytes"`
	AssetDigest string                `json:"asset_digest"`
	AssetURL    string                `json:"asset_url,omitempty"`
}

// ErrNativeCancellationUnsupported preserves work without a provider cancellation API.
var ErrNativeCancellationUnsupported = errors.New("jobs: native inference has no provider cancellation API")

const nativeHeaderBound = 64 << 10

func (r *submissionRecorder) acceptNative(ctx context.Context, answer Acceptance) error {
	result := answer.NativeResult
	if result == nil || !result.State.Terminal() || result.RequestID == "" || int64(len(result.Asset.Bytes)) > r.job.nativeAssetBound {
		return ErrSubmissionUnconfirmed
	}
	if len(result.Asset.Bytes) != 0 && (result.State != JobStateCompleted || result.Asset.ContentType == "") {
		return ErrInvalidJob
	}
	if result.State == JobStateFailed && result.Reason == "" {
		return ErrInvalidJob
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	measurement := copyMeasurement(result.Measurement)
	if measurement != nil {
		id := r.job.ID
		if r.job.ReservationID != "" {
			id = r.job.ReservationID
		}
		measurement.ID = id + ":usage"
		proposed := r.job
		proposed.Measurement = measurement
		if err := proposed.Validate(); err != nil {
			return err
		}
	}
	digest := sha256.Sum256(result.Asset.Bytes)
	receipt := nativeReceipt{Version: 1, JobID: r.job.ID, Account: r.job.Account, Provider: r.job.Provider, Model: r.job.Model, Generation: r.job.CatalogGeneration, RequestID: result.RequestID, State: result.State, Reason: result.Reason, RecordedAt: r.service.now().UTC(), Measurement: measurement, ContentType: result.Asset.ContentType, AssetBytes: int64(len(result.Asset.Bytes)), AssetDigest: hex.EncodeToString(digest[:]), AssetURL: result.AssetURL}
	header, err := json.Marshal(receipt)
	if err != nil || len(header) > nativeHeaderBound {
		return ErrInvalidJob
	}
	var prefix [8]byte
	binary.BigEndian.PutUint64(prefix[:], uint64(len(header)))
	_, err = r.service.assets.Publish(bounded, r.job.nativeReceiptKey, io.MultiReader(bytes.NewReader(prefix[:]), bytes.NewReader(header), bytes.NewReader(result.Asset.Bytes)))
	if errors.Is(err, blob.ErrPublicationExists) {
		retained, _, readErr := r.service.readNativeReceipt(bounded, r.job)
		if readErr != nil {
			return readErr
		}
		// An exact callback retry preserves the first receipt's retention clock.
		receipt.RecordedAt = retained.RecordedAt
		if !reflect.DeepEqual(receipt, retained) {
			return ErrReconciliationConflict
		}
	} else if err != nil {
		return err
	}
	job, err := r.service.recoverNative(bounded, r.job)
	if err != nil {
		return err
	}
	r.job, r.accepted = job, !job.SubmissionPending
	if !r.accepted {
		return ErrSubmissionUnconfirmed
	}
	return nil
}

func (s *Service) readNativeReceipt(ctx context.Context, job Job) (nativeReceipt, []byte, error) {
	reader, err := s.assets.ReadPublished(ctx, job.nativeReceiptKey)
	if err != nil {
		return nativeReceipt{}, nil, err
	}
	defer reader.Close()
	var prefix [8]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return nativeReceipt{}, nil, ErrCorruptRecord
	}
	size := binary.BigEndian.Uint64(prefix[:])
	if size == 0 || size > nativeHeaderBound {
		return nativeReceipt{}, nil, ErrCorruptRecord
	}
	header := make([]byte, size)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nativeReceipt{}, nil, ErrCorruptRecord
	}
	var result nativeReceipt
	if err := json.Unmarshal(header, &result); err != nil {
		return nativeReceipt{}, nil, ErrCorruptRecord
	}
	if result.Version != 1 || result.JobID != job.ID || result.Account != job.Account || result.Provider != job.Provider || result.Model != job.Model || result.Generation != job.CatalogGeneration || result.RequestID == "" || !result.State.Terminal() || result.RecordedAt.Before(job.CreatedAt) || result.AssetBytes < 0 || result.AssetBytes > job.nativeAssetBound {
		return nativeReceipt{}, nil, ErrCorruptRecord
	}
	asset, err := io.ReadAll(io.LimitReader(reader, result.AssetBytes+1))
	digest := sha256.Sum256(asset)
	if err != nil || int64(len(asset)) != result.AssetBytes || hex.EncodeToString(digest[:]) != result.AssetDigest {
		return nativeReceipt{}, nil, ErrCorruptRecord
	}
	return result, asset, nil
}

// recoverNative consumes stored evidence and approved assets without repeating inference.
func (s *Service) recoverNative(ctx context.Context, job Job) (Job, error) {
	if !job.Native || s.assets == nil {
		return job, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case s.nativeRecovery <- struct{}{}:
		defer func() { <-s.nativeRecovery }()
	case <-ctx.Done():
		return job, ctx.Err()
	}
	// A queued recovery can hold an older record while another caller completes it.
	current, err := s.records.Get(ctx, job.Account, job.ID)
	if err != nil {
		return job, err
	}
	job = current
	if job.adminDecision != nil {
		return s.retainAdministratorReceipt(ctx, job)
	}
	if job.AssetExpired(s.now()) {
		return s.expireNativeReceipt(ctx, job)
	}
	if !job.SubmissionPending && (job.AssetKey != "" || job.State != JobStateCompleted) {
		return job, s.assets.Retire(ctx, job.nativeReceiptKey)
	}
	if recovered, found, err := s.recoverPublishedNativeAsset(ctx, job); found || err != nil {
		return recovered, err
	}
	receipt, asset, err := s.readNativeReceipt(ctx, job)
	if errors.Is(err, blob.ErrNotFound) {
		return job, nil
	}
	if err != nil {
		return job, err
	}
	next := job
	next.SubmissionPending = false
	next.providerJobID = receipt.RequestID
	next.Measurement = copyMeasurement(receipt.Measurement)
	if job.SubmissionPending {
		if err := applyReport(&next, Report{State: receipt.State, Reason: receipt.Reason}, receipt.RecordedAt); err != nil {
			return job, err
		}
	}
	if err := next.Validate(); err != nil {
		return job, err
	}
	// Persist usage and terminal state before a separate asset write can fail.
	if job.SubmissionPending {
		if err := s.records.Replace(ctx, job, next); err != nil {
			if !errors.Is(err, storage.ErrConflict) {
				return job, err
			}
			current, readErr := s.records.Get(ctx, job.Account, job.ID)
			if readErr != nil || current.SubmissionPending || current.providerJobID != next.providerJobID || !reflect.DeepEqual(current.Measurement, next.Measurement) {
				return job, err
			}
			next = current
		}
	}
	if next.AssetKey != "" || next.State != JobStateCompleted {
		return next, nil
	}
	if next.AssetExpired(s.now()) {
		return s.expireNativeReceipt(ctx, next)
	}
	contentType := receipt.ContentType
	if len(asset) == 0 {
		if receipt.AssetURL == "" {
			return s.assetRecoveryResult(ctx, next, "invalid")
		}
		if s.externalAssets == nil {
			return s.assetRecoveryResult(ctx, next, "blocked")
		}
		downloaded, fetchErr := s.externalAssets.Fetch(ctx, receipt.AssetURL, next.nativeAssetBound)
		if next.AssetExpired(s.now()) {
			return s.expireNativeReceipt(ctx, next)
		}
		if fetchErr != nil {
			return s.assetRecoveryResult(ctx, next, assetFailureStatus(fetchErr))
		}
		asset, contentType = downloaded.Bytes, downloaded.ContentType
		if len(asset) == 0 || int64(len(asset)) > next.nativeAssetBound || contentType == "" {
			return s.assetRecoveryResult(ctx, next, "invalid")
		}
	}
	// Pin the first accepted bytes before publication. Concurrent downloads cannot replace them.
	digest := sha256.Sum256(asset)
	encoded := hex.EncodeToString(digest[:])
	if next.nativeAssetDigest == "" {
		claimed := next
		claimed.nativeAssetDigest, claimed.nativeAssetContentType = encoded, contentType
		if err := s.records.Replace(ctx, next, claimed); err != nil {
			current, readErr := s.records.Get(ctx, next.Account, next.ID)
			if readErr != nil || current.nativeAssetDigest == "" {
				return next, err
			}
			next = current
		} else {
			next = claimed
		}
	}
	if next.AssetKey != "" {
		return next, nil
	}
	if next.AssetExpired(s.now()) {
		return s.expireNativeReceipt(ctx, next)
	}
	if next.nativeAssetDigest != encoded || next.nativeAssetContentType != contentType {
		return s.assetRecoveryResult(ctx, next, "invalid")
	}
	info, err := s.assets.Publish(ctx, next.nativeAssetKey, bytes.NewReader(asset))
	if errors.Is(err, blob.ErrPublicationExists) {
		recovered, found, readErr := s.recoverPublishedNativeAsset(ctx, next)
		if found || readErr != nil {
			return recovered, readErr
		}
		return next, ErrAssetNotFound
	}
	if err != nil {
		return next, err
	}
	if next.AssetExpired(s.now()) {
		return s.expireNativeReceipt(ctx, next)
	}
	return s.publishNativeAsset(ctx, next, contentType, info.Size, receipt.RecordedAt.Add(job.nativeRetention))
}

// publishNativeAsset preserves concurrent reporting and asset diagnostics during publication.
func (s *Service) publishNativeAsset(ctx context.Context, job Job, contentType string, size int64, expires time.Time) (Job, error) {
	next := job
	for range 4 {
		current, err := s.records.Get(ctx, job.Account, job.ID)
		if err != nil {
			return next, err
		}
		next = current
		if next.AssetKey != "" {
			return next, nil
		}
		if next.AssetExpired(s.now()) {
			return s.expireNativeReceipt(ctx, next)
		}
		if next.nativeAssetDigest != job.nativeAssetDigest || next.nativeAssetContentType != contentType {
			return next, ErrCorruptRecord
		}
		stored := next
		stored.assetRecoveryStatus = ""
		if err := stored.StoreAsset(next.nativeAssetKey, contentType, size, expires); err != nil {
			return next, err
		}
		err = s.records.Replace(ctx, next, stored)
		if err == nil {
			return stored, nil
		}
		current, readErr := s.records.Get(ctx, next.Account, next.ID)
		if readErr == nil && current.AssetKey == stored.AssetKey {
			return current, nil
		}
		if !errors.Is(err, storage.ErrConflict) {
			return next, err
		}
	}
	return next, storage.ErrConflict
}
