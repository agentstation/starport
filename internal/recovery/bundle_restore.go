package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// BundleTargets selects isolated stores and an inactive file staging directory.
// The caller must fence all writers and keep these targets separate from the source.
// FilesDirectory never names an active configuration or catalog directory.
type BundleTargets struct {
	KV             storage.RecordTransfer
	SQL            *sqlstore.DB
	Blobs          blob.RestoreTarget
	FilesDirectory string
}

// PreparedBundle records completed import, not independent history or admission approval.
// Every imported store retains its startup barrier. Selected files remain inactive.
type PreparedBundle struct {
	Version         int            `json:"version"`
	OperationID     string         `json:"operation_id"`
	ManifestSHA256  string         `json:"manifest_sha256"`
	FencingEvidence string         `json:"fencing_evidence,omitempty"`
	Boundary        Record         `json:"boundary"`
	KV              KVImportResult `json:"kv"`
	Files           int            `json:"files"`
}

// PrepareBundle validates the complete source before importing its components.
// Retries require the same operation, manifest, and isolated targets.
// Failure leaves imported stores restricted. Activation is a separate operation.
func PrepareBundle(ctx context.Context, target BundleTargets, request VerifyRequest, operation string, encryption *credentials.EncryptionService) (result PreparedBundle, resultErr error) {
	if err := validateBundlePreparation(target, request, RestoreOperation{ID: operation}); err != nil {
		return result, err
	}
	source, err := InspectRestoreSource(ctx, request, encryption)
	if err != nil {
		return result, err
	}
	return source.Prepare(ctx, target, RestoreOperation{ID: operation})
}

func validateBundlePreparation(target BundleTargets, request VerifyRequest, operation RestoreOperation) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if target.SQL == nil || target.KV == nil || target.Blobs == nil {
		return errors.New("restore requires all storage targets")
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	return validateRestoreFilesPath(target.FilesDirectory, request.Directory, scratch)
}

// Prepare imports only the verified source. Every component rechecks its bytes against the retained digest.
func (s *RestoreSource) Prepare(ctx context.Context, target BundleTargets, operation RestoreOperation) (result PreparedBundle, resultErr error) {
	if s == nil || s.manifest.Format != bundleFormat {
		return result, errors.New("restore source has not passed verification")
	}
	if err := validateBundlePreparation(target, s.request, operation); err != nil {
		return result, err
	}
	request, manifest, references := s.request, s.manifest, s.references
	scratch := request.ScratchDirectory
	// Bind each adapter's import claim to the entire bundle, including selected files.
	identity, err := s.ImportIdentity(operation)
	if err != nil {
		return result, err
	}
	componentOperation := identity.ComponentOperation
	sql, err := prepareVerifiedSQLRestore(ctx, target.SQL, request, componentOperation, scratch, manifest, references)
	if err != nil {
		return result, err
	}
	kv, err := ImportKV(ctx, target.KV, componentOperation, filepath.Join(request.Directory, bundleKVFile), scratch, manifest.KV)
	if err != nil {
		return result, err
	}
	if err := target.Blobs.Restore(ctx, filepath.Join(request.Directory, bundleBlobFile), scratch, componentOperation, manifest.Blobs); err != nil {
		return result, err
	}
	if err := verifyPreparedSQL(ctx, target.SQL, sql.Boundary); err != nil {
		return result, err
	}
	result = PreparedBundle{Version: 1, OperationID: operation.ID, FencingEvidence: operation.FencingEvidence, ManifestSHA256: request.ManifestSHA256, Boundary: sql.Boundary, KV: kv}
	for _, artifact := range manifest.Artifacts {
		if strings.HasPrefix(artifact.Path, "files/") {
			result.Files++
		}
	}
	if err := stageRestoreFiles(ctx, target.FilesDirectory, request.Directory, manifest, result); err != nil {
		return PreparedBundle{}, err
	}
	return result, nil
}

func validateRestoreFilesPath(destination, source, scratch string) error {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return errors.New("restore requires a clean absolute file staging directory")
	}
	if _, err := productfiles.ExistingDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	return CheckRestoreDestinations(source, scratch, destination)
}

// CheckRestoreDestinations refuses overlap with source state or another target.
// It checks lexical paths and native identities before the caller creates missing targets.
func CheckRestoreDestinations(source, scratch string, destinations ...string) error {
	for i, destination := range destinations {
		if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
			return errors.New("restore requires clean absolute target paths")
		}
		pairs := [][2]string{{source, destination}, {destination, source}, {destination, scratch}}
		for _, other := range destinations[:i] {
			pairs = append(pairs, [2]string{other, destination}, [2]string{destination, other})
		}
		if err := checkRestorePathPairs(pairs); err != nil {
			return err
		}
	}
	return nil
}

func checkRestorePathPairs(pairs [][2]string) error {
	for _, pair := range pairs {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return err
		}
		if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("restore file staging overlaps the backup or scratch directory")
		}
		// Native identities also detect path aliases on case-insensitive filesystems.
		inside, err := restoreDirectoryContains(pair[0], pair[1])
		if err != nil {
			return err
		}
		if inside {
			return errors.New("restore file staging aliases the backup or scratch directory")
		}
	}
	return nil
}

func restoreDirectoryContains(parent, child string) (bool, error) {
	identity, err := os.Stat(parent)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for {
		entry, err := os.Stat(child)
		if err == nil && os.SameFile(identity, entry) {
			return true, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		ancestor := filepath.Dir(child)
		if ancestor == child {
			return false, nil
		}
		child = ancestor
	}
}
