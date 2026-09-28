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
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const bundleFormat = "starport-backup-v1"
const bundleManifestFile = "backup-manifest.json"
const bundleKVFile = "kv/kv.db"
const bundleSQLFile = "sql/starport.db"
const bundleBlobFile = "blobs.tar"

const bundleKeyChallenge = "starport-backup-key-access-v1:"

// BundleFile selects one configuration or state file for the private backup.
// ID is a portable relative name. Path never enters diagnostics or the manifest.
type BundleFile struct {
	ID             string
	Path           string
	ExpectedSHA256 string
}

// BundleSources names open adapters for a stopped and externally fenced deployment.
// The SQL witness must remain closed at the supplied boundary throughout capture.
type BundleSources struct {
	KV         storage.RecordSource
	SQL        *sqlstore.DB
	Blobs      blob.Store
	Files      []BundleFile
	Encryption *credentials.EncryptionService
}

// BundleRequest identifies the source boundary and external recovery dependencies.
// FencingEvidence records operator evidence. It does not itself fence a process.
type BundleRequest struct {
	OperationID          string   `json:"operation_id"`
	Build                string   `json:"build"`
	Boundary             Record   `json:"boundary"`
	FencingEvidence      string   `json:"fencing_evidence"`
	KeyReference         string   `json:"key_reference"`
	ExternalRequirements []string `json:"external_requirements"`
}

// BundleArtifact binds a regular file inside the backup to its exact bytes.
type BundleArtifact struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BundleManifest binds captured stores and selected local files. Its existence
// does not approve restoration, validate record references, or authorize admission.
type BundleManifest struct {
	Format       string                  `json:"format"`
	Request      BundleRequest           `json:"request"`
	StartedAt    time.Time               `json:"started_at"`
	FinishedAt   time.Time               `json:"finished_at"`
	KV           KVSnapshot              `json:"kv"`
	SQL          sqlstore.SQLiteSnapshot `json:"sql"`
	Blobs        blob.Snapshot           `json:"blobs"`
	KeyChallenge string                  `json:"key_challenge"`
	Artifacts    []BundleArtifact        `json:"artifacts"`
}

func (r BundleRequest) validate() error {
	for _, value := range []string{r.OperationID, r.Build, r.FencingEvidence, r.KeyReference} {
		if strings.TrimSpace(value) == "" || len(value) > 2048 {
			return errors.New("backup requires bounded operation, build, fencing evidence, and key references")
		}
	}
	if err := validDeployment(r.Boundary.DeploymentID); err != nil {
		return err
	}
	if r.Boundary.Open || r.Boundary.Epoch <= 0 {
		return ErrClosed
	}
	if len(r.ExternalRequirements) > 128 {
		return errors.New("backup has too many external requirements")
	}
	for _, requirement := range r.ExternalRequirements {
		if strings.TrimSpace(requirement) == "" || len(requirement) > 2048 {
			return errors.New("backup has an invalid external requirement")
		}
	}
	return nil
}

// BackupBundle captures portable stores and selected files into a new private directory.
// It publishes the manifest last and leaves incomplete output for diagnosis on error.
// The caller owns inventory completeness, external writer fencing, and reference validation.
func BackupBundle(ctx context.Context, destination string, source BundleSources, request BundleRequest) (manifest BundleManifest, resultErr error) {
	if err := ctx.Err(); err != nil {
		return manifest, err
	}
	if err := request.validate(); err != nil {
		return manifest, err
	}
	if source.KV == nil || source.SQL == nil || source.Blobs == nil || source.Encryption == nil {
		return manifest, errors.New("backup requires all storage adapters and encryption-key access")
	}
	if err := validateBundleFiles(source.Files); err != nil {
		return manifest, err
	}
	witness, err := New(source.SQL)
	if err != nil {
		return manifest, err
	}
	if err := checkBundleBoundary(ctx, witness, request.Boundary); err != nil {
		return manifest, err
	}
	manifest = BundleManifest{Format: bundleFormat, Request: request, StartedAt: time.Now().UTC()}
	manifest.KeyChallenge, err = source.Encryption.EncryptCredential(bundleKeyChallenge + request.OperationID)
	if err != nil {
		return BundleManifest{}, err
	}
	directory, root, err := newKVSnapshotDirectory(destination)
	if err != nil {
		return BundleManifest{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	manifest.KV, err = SnapshotKV(ctx, source.KV, filepath.Join(destination, "kv"))
	if err != nil {
		return BundleManifest{}, err
	}
	sql, err := source.SQL.SnapshotRelational(ctx, filepath.Join(destination, "sql"))
	if err != nil {
		return BundleManifest{}, err
	}
	manifest.SQL = sql.Snapshot
	manifest.Blobs, err = blob.Backup(ctx, source.Blobs, filepath.Join(destination, bundleBlobFile))
	if err != nil {
		return BundleManifest{}, err
	}
	if err := copyBundleFiles(ctx, root, source.Files); err != nil {
		return BundleManifest{}, err
	}
	manifest.Artifacts, err = inspectBundleArtifacts(ctx, root)
	if err != nil {
		return BundleManifest{}, err
	}
	if err := syncBundleDirectories(ctx, root); err != nil {
		return BundleManifest{}, err
	}
	if err := checkBundleBoundary(ctx, witness, request.Boundary); err != nil {
		return BundleManifest{}, err
	}
	manifest.FinishedAt = time.Now().UTC()
	if err := manifest.validate(); err != nil {
		return BundleManifest{}, err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return BundleManifest{}, err
	}
	if len(body) > bundleMaxManifestBytes {
		return BundleManifest{}, errors.New("backup manifest exceeds its size limit")
	}
	if err := directory.CompareAndPublish(ctx, bundleManifestFile, nil, body); err != nil {
		return BundleManifest{}, err
	}
	return manifest, nil
}

func checkBundleBoundary(ctx context.Context, witness *Witness, expected Record) error {
	actual, err := witness.Current(ctx, expected.DeploymentID)
	if err != nil {
		return err
	}
	if actual != expected || actual.Open {
		return ErrConflict
	}
	return nil
}

func validateBundleFiles(files []BundleFile) error {
	if len(files) > bundleMaxArtifacts-5 {
		return errors.New("backup selects too many files")
	}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		if !validBundlePath(file.ID) || seen[file.ID] || !filepath.IsAbs(file.Path) || filepath.Clean(file.Path) != file.Path {
			return errors.New("backup contains an invalid or duplicate file selection")
		}
		if file.ExpectedSHA256 != "" {
			digest, err := hex.DecodeString(file.ExpectedSHA256)
			if err != nil || len(digest) != sha256.Size {
				return errors.New("backup file has an invalid expected digest")
			}
		}
		seen[file.ID] = true
	}
	return nil
}

func copyBundleFiles(ctx context.Context, root *os.Root, files []BundleFile) error {
	for _, file := range files {
		if err := copyBundleFile(ctx, root, file); err != nil {
			return err
		}
	}
	return nil
}

// Digest identifies the published manifest for independent retention by the operator.
func (m BundleManifest) Digest() (string, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
