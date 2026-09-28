package jobs

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/agentstation/starport/internal/blob"
)

var (
	// ErrAssetNotFound reports a job this gateway holds no bytes for. A job that
	// is still running, one that failed, and one whose fetch has not landed all
	// answer with it, because none of them has an asset to serve.
	ErrAssetNotFound = errors.New("jobs: no stored asset")
	// ErrAssetExpired reports a completed job whose bytes passed their retention
	// window. It is separate from ErrAssetNotFound because the two are different
	// facts for a caller: one job never produced an asset, and the other
	// produced one that this gateway no longer keeps.
	ErrAssetExpired = errors.New("jobs: the stored asset expired")
	// ErrAssetTooLarge reports a provider asset above the bound this deployment
	// stores.
	ErrAssetTooLarge = errors.New("jobs: the provider asset exceeds the stored bound")
)

const (
	// DefaultAssetRetention is how long a finished asset stays readable when an
	// operator states no window.
	//
	// A day is short beside the file store's month on purpose. A generated video
	// is an answer a caller collects, not a document it keeps, and both provider
	// families publish their own links with windows measured in hours.
	DefaultAssetRetention = 24 * time.Hour

	// DefaultMaxAssetBytes bounds one stored asset. A provider decides how large
	// its own answer is, and without a bound that decision would size this
	// deployment's storage.
	DefaultMaxAssetBytes int64 = 256 << 20
)

// Asset is the finished output of one job as a provider served it.
//
// The bytes arrive whole rather than as a stream. The gateway fetches an asset
// once, from the single provider that accepted the job, and the bound on the
// size is what makes holding it safe.
type Asset struct {
	ContentType string
	Bytes       []byte
}

// collect fetches and stores the asset of a completed job exactly once.
//
// A failed fetch leaves the record alone and reports nothing. The job did
// complete, and reporting a failure would tell a caller its work failed when it
// did not. The next read retries, so a provider that was briefly unreachable
// costs a later fetch rather than the asset. HasAsset is what stops the retry
// once the bytes land, and the retention window is what ends it if they never
// do.
func (s *Service) collect(ctx context.Context, runner Runner, job Job) Job {
	if job.Native {
		recovered, _ := s.recoverNative(ctx, job)
		return recovered
	}
	if s.assets == nil || job.State != JobStateCompleted || job.HasAsset() {
		return job
	}
	if job.AssetExpired(s.now()) {
		expired, _ := s.expire(ctx, job)
		return expired
	}
	if job.assetPending {
		retained, found, err := s.recoverProviderAsset(ctx, job)
		if found || err != nil {
			return retained
		}
	}
	if runner == nil {
		return job
	}
	bound := s.maxAssetBytes
	if job.assetPending {
		bound = job.AssetBytes
	}
	asset, err := runner.Fetch(ctx, s.handle(job), bound)
	if err != nil || int64(len(asset.Bytes)) > bound {
		return job
	}
	return s.storeProviderAsset(ctx, job, asset)
}

// Open returns one job and a reader over its stored asset.
//
// Expiry is decided on the read rather than by the sweep. The sweep runs on an
// interval, and an asset that answered for the length of that interval past its
// stated window would make the window a suggestion.
func (s *Service) Open(ctx context.Context, account, id string) (Job, io.ReadCloser, error) {
	job, err := s.records.Get(ctx, account, id)
	if err != nil {
		return Job{}, nil, err
	}
	if job.AssetExpired(s.now()) {
		expired, err := s.expire(ctx, job)
		if err != nil {
			return Job{}, nil, err
		}
		return expired, nil, ErrAssetExpired
	}
	if !job.HasAsset() {
		return job, nil, ErrAssetNotFound
	}
	reader, err := s.assets.ReadPublished(ctx, job.AssetKey)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			// The record outlived its bytes. Not found is the honest answer, and
			// it is the same answer a job that never produced one gives.
			return job, nil, ErrAssetNotFound
		}
		return Job{}, nil, fmt.Errorf("jobs: read the asset: %w", err)
	}
	return job, reader, nil
}

// SweepResult counts what one pass reclaimed. An operator reads it to tell a
// deployment with nothing to reclaim from a sweep that never runs.
type SweepResult struct {
	// Scanned counts records read during this invocation.
	Scanned int
	// Released counts records whose slot release this pass confirms.
	Released int
	// Failed counts records that need another recovery attempt.
	Failed int
	// Expired counts jobs whose asset passed its window and went.
	Expired int
	// AwaitingReconciliation counts accepted work beyond automatic polling.
	AwaitingReconciliation int
	// Accounted counts jobs that reached a terminal state without a caller
	// present to settle them.
	Accounted int
}

// Sweep expires assets and retries confirmed terminal cleanup.
// Local polling exhaustion retains provider work and its outstanding capacity.
// A failed record does not prevent recovery of later records.
func (s *Service) Sweep(ctx context.Context) (SweepResult, error) {
	return recoverPages(ctx, &s.recovery, s.records.RecoveryPage, func(ctx context.Context, job Job, result *SweepResult) error {
		swept, err := s.sweepOne(ctx, job, s.now(), result)
		if swept.State.Terminal() {
			settled, settlementErr := s.settleAccounting(ctx, swept)
			if !swept.SlotReleased && settled.SlotReleased {
				result.Released++
			}
			if !swept.Accounted() && settled.Accounted() {
				result.Accounted++
			}
			if settled.SlotID != "" && !settled.SlotReleased && s.meter != nil {
				return errors.Join(err, ErrSlotReleasePending, settlementErr)
			}
			return errors.Join(err, settlementErr)
		}
		return err
	})
}

// sweepOne expires retained assets and reports work that needs reconciliation.
func (s *Service) sweepOne(ctx context.Context, job Job, now time.Time, result *SweepResult) (Job, error) {
	if job.Native {
		var err error
		job, err = s.recoverNative(ctx, job)
		if err != nil {
			return job, err
		}
		if job.SubmissionPending {
			result.AwaitingReconciliation++
		}
	}
	if job.SubmissionPending {
		return job, nil
	}
	if s.policy.Spent(job, now) {
		result.AwaitingReconciliation++
		return job, nil
	}
	if !job.Native && job.assetPending && s.assets != nil && !job.AssetExpired(now) {
		recovered, _, err := s.recoverProviderAsset(ctx, job)
		return recovered, err
	}
	if s.assets == nil || (!job.HasAsset() && !job.assetPending) || !job.AssetExpired(now) {
		return job, nil
	}
	expired, err := s.expire(ctx, job)
	if err != nil {
		return job, err
	}
	result.Expired++
	return expired, nil
}

// expire deletes the bytes and then marks the record.
//
// The bytes go first. An interrupted expiry therefore leaves a record naming
// bytes that may already be gone, which the next pass finishes, rather than an
// object no record names and that nothing can ever find again.
func (s *Service) expire(ctx context.Context, job Job) (Job, error) {
	if job.Native && job.AssetExpired(s.now()) {
		var err error
		job, err = s.expireNativeReceipt(ctx, job)
		if err != nil {
			return job, err
		}
	}
	if job.AssetKey == "" {
		return job, nil
	}
	if err := s.assets.Retire(ctx, job.AssetKey); err != nil {
		return job, fmt.Errorf("jobs: delete the asset: %w", err)
	}
	if !job.AssetExpiredAt.IsZero() {
		return job, nil
	}
	marked := job
	marked.assetPending = false
	if err := marked.ExpireAsset(s.now()); err != nil {
		return Job{}, err
	}
	updated, err := s.commit(ctx, job, marked)
	if err != nil {
		return job, err
	}
	return updated, nil
}

// newAssetKey names the bytes. It is not the job identifier and not derived
// from it, so a leaked identifier names nothing in the byte store, and one
// deployment cannot guess another's objects.
func newAssetKey() string {
	value := uuid.New()
	return hex.EncodeToString(value[:])
}
