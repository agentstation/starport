package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/agentstation/starport/internal/blob"
)

// recoverPublishedNativeAsset verifies retained bytes before any new download.
// The pinned digest and original deadline remain authoritative across restarts.
func (s *Service) recoverPublishedNativeAsset(ctx context.Context, job Job) (Job, bool, error) {
	if job.nativeAssetDigest == "" || job.AssetKey != "" || job.State != JobStateCompleted {
		return job, false, nil
	}
	reader, err := s.assets.ReadPublished(ctx, job.nativeAssetKey)
	if errors.Is(err, blob.ErrNotFound) {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(reader, job.nativeAssetBound+1))
	err = errors.Join(err, reader.Close())
	if err != nil {
		return job, false, err
	}
	if size == 0 || size > job.nativeAssetBound || hex.EncodeToString(digest.Sum(nil)) != job.nativeAssetDigest {
		return job, false, ErrCorruptRecord
	}
	recovered, err := s.publishNativeAsset(ctx, job, job.nativeAssetContentType, size, job.TerminalAt.Add(job.nativeRetention))
	return recovered, true, err
}
