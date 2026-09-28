package recovery

import (
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
)

// CaptureRequest selects an explicit backup destination and operator evidence.
type CaptureRequest struct {
	Destination     string
	OperationID     string
	Build           string
	FencingEvidence string
	KeyReference    string
	EntryLimit      int
}

// Validate refuses incomplete requests before any storage operation.
func (r CaptureRequest) Validate() error {
	if !filepath.IsAbs(r.Destination) || filepath.Clean(r.Destination) != r.Destination {
		return errors.New("backup requires a clean absolute destination")
	}
	for _, value := range []string{r.OperationID, r.Build, r.FencingEvidence, r.KeyReference} {
		if strings.TrimSpace(value) == "" || len(value) > 2048 {
			return errors.New("backup requires bounded operation, build, fencing evidence, and key references")
		}
	}
	if r.EntryLimit < 0 || r.EntryLimit > 100000 {
		return errors.New("backup entry limit must be between zero and 100000")
	}
	return nil
}

// VerifyRequest binds a backup to the digest retained outside that backup.
type VerifyRequest struct {
	Directory      string
	ManifestSHA256 string
}

// Validate checks the path and digest before opening a backup.
func (r VerifyRequest) Validate() error {
	if !filepath.IsAbs(r.Directory) || filepath.Clean(r.Directory) != r.Directory {
		return errors.New("backup verification requires a clean absolute directory")
	}
	digest, err := hex.DecodeString(r.ManifestSHA256)
	if err != nil || len(digest) != 32 {
		return errors.New("backup verification requires the independently retained SHA-256 digest")
	}
	return nil
}

// CaptureResult contains the receipt an operator retains outside the bundle.
// It contains no secret values or credential references.
type CaptureResult struct {
	Directory      string `json:"directory"`
	ManifestSHA256 string `json:"manifest_sha256"`
	DeploymentID   string `json:"deployment_id"`
	RecoveryEpoch  int64  `json:"recovery_epoch"`
	Artifacts      int    `json:"artifacts"`
}
