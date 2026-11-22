package recovery

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/catalogs/storage"
	"github.com/agentstation/starmap/pkg/productfiles"
)

// CapturedBoundary returns the original closed backup boundary.
// It grants no target admission permission.
func (s *RestoreSource) CapturedBoundary() Record {
	if s == nil {
		return Record{}
	}
	return s.manifest.Request.Boundary
}

// ManifestDigest returns the original verified bundle identity.
func (s *RestoreSource) ManifestDigest() string {
	if s == nil {
		return ""
	}
	return s.request.ManifestSHA256
}

// OpenCapturedKV checks an owned copy of the original immutable backup image.
// The caller closes the read-only view. No target store or current clock participates.
func (s *RestoreSource) OpenCapturedKV(ctx context.Context) (*KVSnapshotView, error) {
	if ctx == nil || s == nil || s.manifest.Format != bundleFormat {
		return nil, errors.New("restore requires a verified source and context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return OpenKVSnapshot(ctx, filepath.Join(s.request.Directory, filepath.FromSlash(bundleKVFile)), s.request.ScratchDirectory, s.manifest.KV)
}

// SelectedPayload checks bounded original selected-file bytes against the verified manifest.
// Metadata retains its smaller bound through SelectedFile.
func (s *RestoreSource) SelectedPayload(ctx context.Context, id string, limit int64) ([]byte, error) {
	if limit <= 0 || limit > 2*storage.DefaultRetentionInputMaxBytes {
		return nil, errors.New("restore payload exceeds its byte limit")
	}
	return s.selectedFile(ctx, id, limit)
}

// checkCapturedManifest rechecks the original root receipt without reading a configured target.
// Component owners still verify every image used by the operation.
func (s *RestoreSource) checkCapturedManifest(ctx context.Context) error {
	if ctx == nil || s == nil || s.manifest.Format != bundleFormat {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := productfiles.ExistingDirectory(s.request.Directory)
	if err != nil {
		return err
	}
	body, err := directory.ReadFile(bundleManifestFile, bundleMaxManifestBytes)
	if err != nil {
		return err
	}
	if historySHA256(body) != s.ManifestDigest() {
		return ErrConflict
	}
	return nil
}
