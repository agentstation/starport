package recovery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/agentstation/starport/internal/credentials"
)

// RestoreOperation identifies one preparation and the operator's external fencing evidence.
// The evidence reference does not itself fence any process.
type RestoreOperation struct {
	ID              string
	FencingEvidence string
}

// Validate bounds the operation fields before any target mutation.
func (o RestoreOperation) Validate() error {
	if strings.TrimSpace(o.ID) == "" || len(o.ID) > 128 || strings.ContainsFunc(o.ID, unicode.IsControl) {
		return errors.New("restore requires a bounded operation identifier")
	}
	if len(o.FencingEvidence) > 2048 || strings.ContainsFunc(o.FencingEvidence, unicode.IsControl) {
		return errors.New("restore fencing evidence must contain at most 2048 bytes without control characters")
	}
	return nil
}

// RestoreSource retains verified backup facts without exposing mutable manifest fields.
// Inspect it before opening target stores. Preparation still rechecks each component's bytes.
type RestoreSource struct {
	request    VerifyRequest
	manifest   BundleManifest
	references ReferenceReport
}

// InspectRestoreSource checks the complete bundle, retained references, and encryption-key access.
// It opens no target and grants no admission permission.
func InspectRestoreSource(ctx context.Context, request VerifyRequest, encryption *credentials.EncryptionService, inspectors ...CapturedKVInspector) (*RestoreSource, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if request.ScratchDirectory == "" {
		request.ScratchDirectory = filepath.Dir(request.Directory)
	}
	manifest, references, err := InspectBundleReferences(ctx, request.Directory, request.ManifestSHA256, request.ScratchDirectory, encryption, inspectors...)
	if err != nil {
		return nil, err
	}
	return &RestoreSource{request: request, manifest: manifest, references: references}, nil
}

// CheckOriginalArtifacts rechecks the exact source bytes and current encryption-key access.
// Unchanged artifacts retain their previously checked domain references. No target or permission changes.
func (s *RestoreSource) CheckOriginalArtifacts(ctx context.Context, request VerifyRequest, encryption *credentials.EncryptionService) error {
	if ctx == nil || s == nil || s.manifest.Format != bundleFormat {
		return ErrConflict
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if request.ScratchDirectory == "" {
		request.ScratchDirectory = filepath.Dir(request.Directory)
	}
	if request != s.request {
		return ErrConflict
	}
	_, err := VerifyBundle(ctx, s.request.Directory, s.request.ManifestSHA256, encryption)
	return err
}

// DeploymentID identifies the verified source deployment.
func (s *RestoreSource) DeploymentID() string { return s.manifest.Request.Boundary.DeploymentID }

// References reports captured-state consistency and unresolved history.
func (s *RestoreSource) References() ReferenceReport { return s.references }

// PrepareRequest selects a verified backup and an inactive file staging directory.
// Target adapters come from the operator's current product configuration.
type PrepareRequest struct {
	VerifyRequest
	Operation      RestoreOperation
	FilesDirectory string
}

// Validate refuses incomplete operator requests before configuration or target access.
func (r PrepareRequest) Validate() error {
	if err := r.VerifyRequest.Validate(); err != nil {
		return err
	}
	if err := r.Operation.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Operation.FencingEvidence) == "" {
		return errors.New("restore requires external writer-fencing evidence")
	}
	if !filepath.IsAbs(r.FilesDirectory) || filepath.Clean(r.FilesDirectory) != r.FilesDirectory {
		return errors.New("restore requires a clean absolute file staging directory")
	}
	return nil
}

// PrepareResult reports staged state and unresolved history, never activation approval.
type PrepareResult struct {
	Prepared       PreparedBundle    `json:"prepared"`
	FilesDirectory string            `json:"files_directory"`
	References     ReferenceReport   `json:"references"`
	FilePlan       []FileDisposition `json:"file_plan"`
}
