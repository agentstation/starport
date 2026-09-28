package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// SelectedFile reads bounded metadata from a verified source and checks its bytes again.
// Names are portable bundle IDs. Backup paths cannot select arbitrary host files.
func (s *RestoreSource) SelectedFile(ctx context.Context, id string, limit int64) ([]byte, error) {
	if s == nil || s.manifest.Format != bundleFormat || !validBundlePath(id) || limit < 0 || limit > bundleMaxManifestBytes {
		return nil, errors.New("restore requires verified bounded file metadata")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, artifact := range s.manifest.Artifacts {
		if artifact.Path != "files/"+id {
			continue
		}
		if artifact.Size > limit {
			return nil, errors.New("restore metadata exceeds its byte limit")
		}
		directory, err := productfiles.ExistingDirectory(s.request.Directory)
		if err != nil {
			return nil, err
		}
		parts := strings.Split(artifact.Path, "/")
		for _, part := range parts[:len(parts)-1] {
			directory, err = directory.ExistingChild(part)
			if err != nil {
				return nil, err
			}
		}
		body, err := directory.ReadFile(parts[len(parts)-1], limit)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(body)
		if int64(len(body)) != artifact.Size || hex.EncodeToString(digest[:]) != artifact.SHA256 {
			return nil, errors.New("restore metadata changed after verification")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return body, nil
	}
	return nil, errors.New("restore metadata is absent from the verified manifest")
}

// SelectedFileHashes returns a separate map of verified selected-file digests.
func (s *RestoreSource) SelectedFileHashes() map[string]string {
	result := make(map[string]string)
	if s != nil {
		for _, artifact := range s.manifest.Artifacts {
			if id, ok := strings.CutPrefix(artifact.Path, "files/"); ok {
				result[id] = artifact.SHA256
			}
		}
	}
	return result
}

// FileDisposition reports the target location and the procedure that must approve publication.
// It contains private operator paths, but no file contents or credential values.
type FileDisposition struct {
	ArtifactID  string `json:"artifact_id"`
	Role        string `json:"role"`
	Relative    string `json:"relative"`
	SHA256      string `json:"sha256"`
	Destination string `json:"destination,omitempty"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
}
