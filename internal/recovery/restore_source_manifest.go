package recovery

import (
	"context"

	"github.com/agentstation/starmap/pkg/productfiles"
)

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
