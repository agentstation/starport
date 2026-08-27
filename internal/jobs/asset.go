package jobs

import (
	"bytes"
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
	if s.assets == nil || runner == nil {
		return job
	}
	if job.State != JobStateCompleted || job.AssetKey != "" {
		return job
	}
	asset, err := runner.Fetch(ctx, s.handle(job), s.maxAssetBytes)
	if err != nil {
		return job
	}
	key := newAssetKey()
	info, err := s.assets.Put(ctx, key, bytes.NewReader(asset.Bytes))
	if err != nil {
		return job
	}
	stored := job
	if err := stored.StoreAsset(key, asset.ContentType, info.Size, s.now().Add(s.retention)); err != nil {
		s.discard(ctx, key)
		return job
	}
	if err := s.records.Replace(ctx, stored); err != nil {
		// The record is the only thing that names the bytes, so bytes no record
		// names are unreachable and go now rather than at a sweep that would
		// never find them.
		s.discard(ctx, key)
		return job
	}
	return stored
}

// Open returns one job and a reader over its stored asset.
//
// Expiry is decided on the read rather than by the sweep. The sweep runs on an
// interval, and an asset that answered for the length of that interval past its
// stated window would make the window a suggestion.
func (s *Service) Open(ctx context.Context, tenant, id string) (Job, io.ReadCloser, error) {
	job, err := s.records.Get(ctx, tenant, id)
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
	reader, err := s.assets.Get(ctx, job.AssetKey)
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
	// Expired counts jobs whose asset passed its window and went.
	Expired int
}

// Sweep reclaims the asset storage that nothing may read any more.
//
// The record stays. A completed job stays completed after its bytes go, because
// the work happened and the tenant paid for it, and the expiry marker is what
// separates the two answers a caller reads.
//
// One failing record does not stop the pass. A sweep that returned on the first
// error would let one unreachable object hold every later one hostage, and the
// caller runs on a ticker that would repeat the same failure forever.
func (s *Service) Sweep(ctx context.Context) (SweepResult, error) {
	if s.assets == nil {
		return SweepResult{}, nil
	}
	records, err := s.records.Scan(ctx, 0)
	if err != nil {
		return SweepResult{}, err
	}
	now := s.now()
	var result SweepResult
	var failures error
	for _, job := range records {
		if !job.HasAsset() || !job.AssetExpired(now) {
			continue
		}
		if _, err := s.expire(ctx, job); err != nil {
			failures = errors.Join(failures, fmt.Errorf("jobs: sweep %s: %w", job.ID, err))
			continue
		}
		result.Expired++
	}
	return result, failures
}

// expire deletes the bytes and then marks the record.
//
// The bytes go first. An interrupted expiry therefore leaves a record naming
// bytes that may already be gone, which the next pass finishes, rather than an
// object no record names and that nothing can ever find again.
func (s *Service) expire(ctx context.Context, job Job) (Job, error) {
	if job.AssetKey == "" {
		return job, nil
	}
	if err := s.assets.Delete(ctx, job.AssetKey); err != nil && !errors.Is(err, blob.ErrNotFound) {
		return Job{}, fmt.Errorf("jobs: delete the asset: %w", err)
	}
	if !job.AssetExpiredAt.IsZero() {
		return job, nil
	}
	marked := job
	if err := marked.ExpireAsset(s.now()); err != nil {
		return Job{}, err
	}
	return s.commit(ctx, marked)
}

// discard removes bytes no record names and reports nothing. Every caller is
// already unwinding from a failure it is about to report or absorb.
func (s *Service) discard(ctx context.Context, key string) {
	_ = s.assets.Delete(ctx, key)
}

// newAssetKey names the bytes. It is not the job identifier and not derived
// from it, so a leaked identifier names nothing in the byte store, and one
// deployment cannot guess another's objects.
func newAssetKey() string {
	value := uuid.New()
	return hex.EncodeToString(value[:])
}
