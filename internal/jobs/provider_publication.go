package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

// ErrAssetRetirementRequired preserves ownership until byte retirement succeeds.
var ErrAssetRetirementRequired = errors.New("jobs: retire asset bytes before deleting the record")

func (j Job) validateAssetPublication() error {
	if j.assetDigest == "" {
		if j.assetPending {
			return ErrInvalidJob
		}
		return nil
	}
	digest, err := hex.DecodeString(j.assetDigest)
	if err != nil || len(digest) != sha256.Size || j.Native || j.AssetKey == "" || j.AssetBytes < 0 || j.State != JobStateCompleted {
		return ErrInvalidJob
	}
	return nil
}

// storeProviderAsset binds one identity and content digest before byte publication.
// Lost acknowledgments retain a discoverable candidate for refresh or startup recovery.
func (s *Service) storeProviderAsset(ctx context.Context, job Job, asset Asset) Job {
	digest := sha256.Sum256(asset.Bytes)
	encoded := hex.EncodeToString(digest[:])
	if job.AssetKey == "" {
		prepared := job
		if err := prepared.StoreAsset(newAssetKey(), asset.ContentType, int64(len(asset.Bytes)), s.now().Add(s.retention)); err != nil {
			return job
		}
		prepared.assetPending, prepared.assetDigest = true, encoded
		if err := s.records.Replace(ctx, job, prepared); err != nil {
			current, readErr := s.records.Get(ctx, job.Account, job.ID)
			if readErr != nil || current.AssetKey == "" {
				return job
			}
			job = current
		} else {
			job = prepared
		}
	}
	if job.AssetExpired(s.now()) {
		expired, _ := s.expire(ctx, job)
		return expired
	}
	if job.HasAsset() {
		return job
	}
	if !job.assetPending || job.assetDigest != encoded || job.AssetBytes != int64(len(asset.Bytes)) || job.AssetContentType != asset.ContentType {
		return job
	}
	_, err := s.assets.Publish(ctx, job.AssetKey, bytes.NewReader(asset.Bytes))
	if err != nil && !errors.Is(err, blob.ErrPublicationExists) {
		return job
	}
	recovered, _, _ := s.recoverProviderAsset(ctx, job)
	return recovered
}

// recoverProviderAsset uses retained bytes without another provider fetch.
func (s *Service) recoverProviderAsset(ctx context.Context, job Job) (Job, bool, error) {
	if !job.assetPending {
		return job, false, nil
	}
	if job.AssetExpired(s.now()) {
		expired, err := s.expire(ctx, job)
		return expired, true, err
	}
	reader, err := s.assets.ReadPublished(ctx, job.AssetKey)
	if errors.Is(err, blob.ErrNotFound) {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	digest := sha256.New()
	// The declared size comes from bounded, measured input before preparation.
	size, err := io.Copy(digest, io.LimitReader(reader, job.AssetBytes+1))
	err = errors.Join(err, reader.Close())
	if err != nil {
		return job, false, err
	}
	if size != job.AssetBytes || hex.EncodeToString(digest.Sum(nil)) != job.assetDigest {
		return job, false, ErrCorruptRecord
	}
	for range 4 {
		current, err := s.records.Get(ctx, job.Account, job.ID)
		if err != nil {
			return job, false, err
		}
		if current.AssetExpired(s.now()) {
			expired, err := s.expire(ctx, current)
			return expired, true, err
		}
		if current.AssetKey != job.AssetKey || current.assetDigest != job.assetDigest {
			return current, false, ErrCorruptRecord
		}
		if !current.assetPending {
			return current, true, nil
		}
		ready := current
		ready.assetPending = false
		err = s.records.Replace(ctx, current, ready)
		if err == nil {
			return ready, true, nil
		}
		// Read again after an ambiguous acknowledgment. Never create another identity.
		accepted, readErr := s.records.Get(ctx, job.Account, job.ID)
		if readErr == nil && accepted.AssetKey == job.AssetKey && !accepted.assetPending && accepted.assetDigest == job.assetDigest {
			return accepted, true, nil
		}
		if !errors.Is(err, storage.ErrConflict) {
			return current, false, err
		}
	}
	return job, false, storage.ErrConflict
}
