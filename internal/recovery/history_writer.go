package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/authorization/revision"
)

const (
	historyManifestFile     = "history.json"
	historyPayloadDirectory = "payloads"
)

// HistoryEvidenceFile names one retained original evidence file.
// The writer records its digest and size. It does not interpret or copy its bytes.
type HistoryEvidenceFile struct {
	ID        string
	Path      string
	Reference string
}

// HistoryWriteRequest selects a final-only history package and its external facts.
// The caller derives TargetSHA256 and SQLExpected from the fenced target, not from operator input.
// The attestation authorizes the write. The package does not store it.
type HistoryWriteRequest struct {
	Directory      string
	Operation      RestoreOperation
	TargetSHA256   string
	SQLExpected    *revision.Stamp
	Mode           string
	Disposition    string
	Through        time.Time
	EndReference   string
	HighestEpoch   int64
	EpochReference string
	EpochOperator  string
	EpochEvidence  string
	Evidence       []HistoryEvidenceFile
	Attestation    HistoryAttestation
}

// Validate refuses incomplete operator input before any source, target, or file access.
// It does not check the derived target and SQL bindings.
func (r HistoryWriteRequest) Validate() error {
	if !filepath.IsAbs(r.Directory) || filepath.Clean(r.Directory) != r.Directory {
		return errors.New("history writing requires a clean absolute package directory")
	}
	if err := r.Operation.Validate(); err != nil {
		return err
	}
	if !historyReference(r.Operation.FencingEvidence, 2048) {
		return errors.New("history writing requires the unchanged fencing reference")
	}
	if r.Mode != "planned_migration" && r.Mode != "disaster_recovery" {
		return errors.New("history writing requires the planned_migration or disaster_recovery mode")
	}
	if r.Disposition != historyReplayComplete && r.Disposition != "remain_restricted" {
		return errors.New("history writing requires the replay_complete or remain_restricted disposition")
	}
	if r.Through.IsZero() || !historyReference(r.EndReference, 2048) {
		return errors.New("history writing requires an interval end time and reference")
	}
	if r.HighestEpoch < 0 || r.HighestEpoch >= math.MaxInt64-1 || !historyReference(r.EpochReference, 4096) || !historyReference(r.EpochOperator, 4096) {
		return errors.New("history writing requires a bounded highest epoch with its reference and operator")
	}
	if len(r.Evidence) == 0 || len(r.Evidence) > historyMaxEntries {
		return errors.New("history writing requires between one and 4096 evidence files")
	}
	seen := make(map[string]bool, len(r.Evidence))
	for _, file := range r.Evidence {
		if !historyReference(file.ID, 128) || seen[file.ID] || !historyReference(file.Reference, 2048) {
			return errors.New("history writing requires unique bounded evidence identifiers and references")
		}
		if !filepath.IsAbs(file.Path) || filepath.Clean(file.Path) != file.Path {
			return errors.New("history writing requires clean absolute evidence file paths")
		}
		seen[file.ID] = true
	}
	if !seen[r.EpochEvidence] {
		return errors.New("history writing requires the epoch evidence to name one evidence file")
	}
	a := r.Attestation
	if !historyReference(a.Operator, 4096) || !historyReference(a.Reference, 4096) || !a.WritersFenced || !a.AdmittedWorkAccounted || !a.CompleteInterval {
		return errors.New("history writing requires an operator, evidence reference, fenced writers, accounted admitted work, and a complete interval")
	}
	return nil
}

// WriteFinalHistory writes a package whose only steps are the final KV and SQL authority rotations.
// It requires an existing empty private directory, publishes history.json last, and verifies its own output.
// An interrupted write leaves no manifest. The operator removes the partial directory before a retry.
func (s *RestoreSource) WriteFinalHistory(ctx context.Context, request HistoryWriteRequest) (*VerifiedHistory, error) {
	return s.writeFinalHistory(ctx, request, nil)
}

func (s *RestoreSource) writeFinalHistory(ctx context.Context, request HistoryWriteRequest, checkpoint func(string) error) (*VerifiedHistory, error) {
	if ctx == nil || s == nil || s.manifest.Format != bundleFormat {
		return nil, ErrConflict
	}
	if err := request.Validate(); err != nil {
		return nil, errors.Join(ErrConflict, err)
	}
	if !historyDigest(request.TargetSHA256) || revision.ValidateSQLRecoveryPreimage(request.SQLExpected) != nil {
		return nil, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	identity, err := s.ImportIdentity(request.Operation)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(identity, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	payloads, err := s.finalHistoryPayloads(ctx, request.SQLExpected)
	if err != nil {
		return nil, err
	}
	manifest, err := s.finalHistoryManifest(ctx, request, historySHA256(encoded), payloads)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(manifest, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if len(body) > historyManifestMaxBytes {
		return nil, errors.New("recovery history manifest exceeds its byte limit")
	}
	if err := publishFinalHistory(ctx, request.Directory, payloads, body, checkpoint); err != nil {
		return nil, err
	}
	return s.VerifyHistoryPackage(ctx, HistoryPackageRequest{Directory: request.Directory, ManifestSHA256: historySHA256(body), TargetSHA256: request.TargetSHA256, Operation: request.Operation})
}

// The KV expectation comes from the immutable backup capture. The SQL expectation comes from the caller.
func (s *RestoreSource) finalHistoryPayloads(ctx context.Context, sqlExpected *revision.Stamp) ([][]byte, error) {
	view, err := s.OpenCapturedKV(ctx)
	if err != nil {
		return nil, err
	}
	_, kvExpected, err := revision.CaptureKVRecovery(ctx, view)
	if err = errors.Join(err, view.Close()); err != nil {
		return nil, err
	}
	kv, err := json.Marshal(historyKVAuthorityPayload{Version: 1, ExpectedSHA256: kvExpected}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	sql, err := json.Marshal(historySQLAuthorityPayload{Version: 1, Expected: sqlExpected}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	return [][]byte{kv, sql}, nil
}

// finalHistoryManifest applies the verifier's own validation before any file changes.
func (s *RestoreSource) finalHistoryManifest(ctx context.Context, request HistoryWriteRequest, preparedSHA256 string, payloads [][]byte) (historyManifest, error) {
	manifest := historyManifest{
		Version: 1, BackupSHA256: s.ManifestDigest(), DeploymentID: s.DeploymentID(), Operation: request.Operation, TargetSHA256: request.TargetSHA256, PreparedSHA256: preparedSHA256,
		Mode: request.Mode, Disposition: request.Disposition, Interval: historyInterval{Through: request.Through.UTC(), EndReference: request.EndReference},
		HighestEpoch: EpochEvidence{HighestEpoch: request.HighestEpoch, Reference: request.EpochReference, Operator: request.EpochOperator},
	}
	ids := make([]string, 0, len(request.Evidence))
	for _, file := range request.Evidence {
		evidence, err := historyEvidenceFile(ctx, file)
		if err != nil {
			return historyManifest{}, err
		}
		if file.ID == request.EpochEvidence {
			manifest.HighestEpoch.SourceSHA256 = evidence.SHA256
		}
		manifest.Evidence = append(manifest.Evidence, evidence)
		ids = append(ids, file.ID)
	}
	for i, kind := range []string{historyKVAuthorityFinal, historySQLAuthorityFinal} {
		manifest.Steps = append(manifest.Steps, historyStep{Ordinal: i + 1, Kind: kind, Path: fmt.Sprintf("payloads/%06d.json", i+1), Size: len(payloads[i]), SHA256: historySHA256(payloads[i]), Evidence: ids})
	}
	if err := manifest.validate(s, HistoryPackageRequest{TargetSHA256: request.TargetSHA256, Operation: request.Operation}, preparedSHA256); err != nil {
		return historyManifest{}, err
	}
	return manifest, nil
}

// historyEvidenceFile digests one regular file and refuses links and replaced files.
func historyEvidenceFile(ctx context.Context, file HistoryEvidenceFile) (_ historyEvidence, resultErr error) {
	if err := ctx.Err(); err != nil {
		return historyEvidence{}, err
	}
	before, err := os.Lstat(file.Path)
	if err != nil {
		return historyEvidence{}, err
	}
	if !before.Mode().IsRegular() {
		return historyEvidence{}, fmt.Errorf("recovery history evidence %q is not a regular file", file.ID)
	}
	opened, err := os.Open(file.Path)
	if err != nil {
		return historyEvidence{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, opened.Close()) }()
	after, err := opened.Stat()
	if err != nil {
		return historyEvidence{}, err
	}
	if !os.SameFile(before, after) {
		return historyEvidence{}, fmt.Errorf("recovery history evidence %q changed while it was opened", file.ID)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, opened)
	if err != nil {
		return historyEvidence{}, err
	}
	return historyEvidence{ID: file.ID, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size, Reference: file.Reference}, nil
}

// publishFinalHistory writes payloads before the manifest, so an interrupted write has no manifest.
func publishFinalHistory(ctx context.Context, directory string, payloads [][]byte, manifest []byte, checkpoint func(string) error) error {
	root, err := productfiles.ExistingDirectory(directory)
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	opened, err := root.Open()
	if err != nil {
		return err
	}
	names, readErr := fs.ReadDir(opened.FS(), ".")
	err = errors.Join(readErr, opened.Close())
	if err != nil || len(names) != 0 {
		return errors.Join(ErrConflict, err)
	}
	children, err := root.CreateChild(historyPayloadDirectory)
	if err != nil {
		return err
	}
	publish := func(target *productfiles.Directory, name string, body []byte) error {
		if checkpoint != nil {
			if err := checkpoint(name); err != nil {
				return err
			}
		}
		return target.CompareAndPublish(ctx, path.Base(name), nil, body)
	}
	for i, payload := range payloads {
		if err := publish(children, fmt.Sprintf("payloads/%06d.json", i+1), payload); err != nil {
			return err
		}
	}
	return publish(root, historyManifestFile, manifest)
}
