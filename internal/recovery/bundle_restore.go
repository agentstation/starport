package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"

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
	Version        int            `json:"version"`
	OperationID    string         `json:"operation_id"`
	ManifestSHA256 string         `json:"manifest_sha256"`
	Boundary       Record         `json:"boundary"`
	KV             KVImportResult `json:"kv"`
	Files          int            `json:"files"`
}

// PrepareBundle validates the complete source before importing its components.
// Retries require the same operation, manifest, and isolated targets.
// Failure leaves imported stores restricted. Activation is a separate operation.
func PrepareBundle(ctx context.Context, target BundleTargets, request VerifyRequest, operation string, encryption *credentials.EncryptionService) (result PreparedBundle, resultErr error) {
	if err := request.Validate(); err != nil {
		return result, err
	}
	if target.SQL == nil || target.KV == nil || target.Blobs == nil {
		return result, errors.New("restore requires all storage targets")
	}
	if strings.TrimSpace(operation) == "" || len(operation) > 128 || strings.ContainsFunc(operation, unicode.IsControl) {
		return result, errors.New("restore requires a bounded operation identifier")
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	if err := validateRestoreFilesPath(target.FilesDirectory, request.Directory, scratch); err != nil {
		return result, err
	}
	manifest, references, err := InspectBundleReferences(ctx, request.Directory, request.ManifestSHA256, scratch, encryption)
	if err != nil {
		return result, err
	}
	// Bind each adapter's import claim to the entire bundle, including selected files.
	identity, err := json.Marshal(struct {
		Version             int
		Operation, Manifest string
	}{1, operation, request.ManifestSHA256})
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(identity)
	componentOperation := hex.EncodeToString(digest[:])
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
	result = PreparedBundle{Version: 1, OperationID: operation, ManifestSHA256: request.ManifestSHA256, Boundary: sql.Boundary, KV: kv}
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
	// Keep the backup and scratch trees outside the destination.
	for _, pair := range [][2]string{{source, destination}, {destination, source}, {destination, scratch}} {
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
