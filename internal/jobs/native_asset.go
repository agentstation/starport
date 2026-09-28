package jobs

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrAssetDownloadBlocked reports a reference without an explicit destination grant.
	ErrAssetDownloadBlocked = errors.New("jobs: asset download origin is not approved")
	// ErrAssetDownloadUnavailable reports a transfer that can retry within retention.
	ErrAssetDownloadUnavailable = errors.New("jobs: asset download is unavailable")
	// ErrAssetDownloadInvalid reports an invalid external asset response.
	ErrAssetDownloadInvalid = errors.New("jobs: asset download is invalid")
)

// ExternalAssetFetcher owns destination authorization and bounded asset transfers.
// It has no inference credential input and cannot submit generation requests.
type ExternalAssetFetcher interface {
	Fetch(context.Context, string, int64) (Asset, error)
}

// WithExternalAssets enables separately authorized asset transfers.
func WithExternalAssets(fetcher ExternalAssetFetcher) ServiceOption {
	return func(s *Service) { s.externalAssets = fetcher }
}

// AssetStatus reports retrieval state without disclosing a provider reference.
func (j Job) AssetStatus(now time.Time) string {
	switch {
	case j.AssetExpired(now):
		return assetRecoveryExpired
	case j.HasAsset():
		return "stored"
	case j.assetPending:
		return "pending"
	case !j.Native || j.State != JobStateCompleted:
		return ""
	case j.assetRecoveryStatus != "":
		return j.assetRecoveryStatus
	default:
		return "pending"
	}
}

func (s *Service) assetRecoveryResult(ctx context.Context, job Job, status string) (Job, error) {
	if job.assetRecoveryStatus == status {
		return job, nil
	}
	next := job
	next.assetRecoveryStatus = status
	if err := s.records.Replace(ctx, job, next); err != nil {
		return job, err
	}
	return next, nil
}

func assetFailureStatus(err error) string {
	switch {
	case errors.Is(err, ErrAssetDownloadBlocked):
		return "blocked"
	case errors.Is(err, ErrAssetDownloadInvalid), errors.Is(err, ErrAssetTooLarge):
		return "invalid"
	default:
		return "retry"
	}
}

func (s *Service) expireNativeReceipt(ctx context.Context, job Job) (Job, error) {
	if err := s.assets.Retire(ctx, job.nativeReceiptKey); err != nil {
		return job, err
	}
	// The preallocated asset key also covers an interrupted final publication.
	if err := s.assets.Retire(ctx, job.nativeAssetKey); err != nil {
		return job, err
	}
	return s.assetRecoveryResult(ctx, job, assetRecoveryExpired)
}
