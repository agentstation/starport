package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

const (
	historyProjectionKVDirectory          = "result-kv"
	historyProjectionBindingFile          = "binding.json"
	historyProjectionRecordFile           = "projection.json"
	historyProjectionPublicationDirectory = ".record-publications"
	historyProjectionBlobLive             = "live"
)

// HistoryProjectionRequest selects an offline workspace and original independent evidence.
// It contains no configured target adapters or permission to reopen a deployment.
type HistoryProjectionRequest struct {
	Directory   string
	History     HistoryPackageRequest
	Attestation HistoryAttestation
}

type historyProjectionBinding struct {
	Version      int                   `json:"version"`
	BackupSHA256 string                `json:"backup_sha256"`
	DeploymentID string                `json:"deployment_id"`
	History      HistoryPackageRequest `json:"history"`
	Through      time.Time             `json:"through_utc"`
	Attestation  HistoryAttestation    `json:"attestation"`
	KVRevision   string                `json:"kv_revision_sha256"`
	SQLRevision  *revision.Stamp       `json:"sql_revision"`
}

type historyProjectionRecord struct {
	Version       int                               `json:"version"`
	BindingSHA256 string                            `json:"binding_sha256"`
	PrefixSteps   int                               `json:"prefix_steps"`
	KV            KVSnapshot                        `json:"kv"`
	SQL           sqlstore.SQLiteSnapshot           `json:"sql"`
	SQLCensus     sqlstore.RelationalRecoveryCensus `json:"sql_census"`
	Blobs         blob.Snapshot                     `json:"blobs"`
}

// IndependentHistoryProjection retains an expected state derived only from original evidence.
// Native authority, catalog publication evidence, and external fencing remain separate requirements.
type IndependentHistoryProjection struct {
	state *independentHistoryProjectionState
}

type independentHistoryProjectionState struct {
	directory string
	binding   historyProjectionBinding
	bindBytes []byte
	record    historyProjectionRecord
	bytes     []byte
}

// Format omits original evidence, private paths, and retained state.
func (IndependentHistoryProjection) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, "<private independent history projection>")
}

// ProjectIndependentHistory reconstructs KV, SQL, and blobs in an offline private workspace.
// It preserves original expiration and through-time, and stops before both final authorization rotations.
// Completed retries inspect retained original outputs. Interrupted workspaces remain restricted for inspection.
func ProjectIndependentHistory(ctx context.Context, source *RestoreSource, request HistoryProjectionRequest, encryption *credentials.EncryptionService) (_ *IndependentHistoryProjection, resultErr error) {
	if ctx == nil || source == nil || encryption == nil || !filepath.IsAbs(request.Directory) || filepath.Clean(request.Directory) != request.Directory ||
		!request.Attestation.WritersFenced || !request.Attestation.AdmittedWorkAccounted || !request.Attestation.CompleteInterval ||
		!historyReference(request.Attestation.Operator, 4096) || !historyReference(request.Attestation.Reference, 4096) {
		return nil, ErrConflict
	}
	if err := CheckRestoreDestinations(source.request.Directory, source.request.ScratchDirectory, request.Directory); err != nil {
		return nil, err
	}
	if err := CheckRestoreDestinations(request.History.Directory, source.request.ScratchDirectory, request.Directory); err != nil {
		return nil, err
	}
	if _, err := VerifyBundle(ctx, source.request.Directory, source.ManifestDigest(), encryption); err != nil {
		return nil, err
	}
	history, err := source.VerifyHistoryPackage(ctx, request.History)
	if err != nil {
		return nil, err
	}
	binding, bindBytes, stop, err := deriveHistoryProjectionBinding(source, request, history)
	if err != nil {
		return nil, err
	}
	directory, err := productfiles.ExistingDirectory(request.Directory)
	if err != nil {
		return nil, err
	}
	completed, exists, err := readCompletedHistoryProjection(ctx, directory, request, binding, bindBytes, stop)
	if exists || err != nil {
		return completed, err
	}
	root, err := directory.Open()
	if err != nil {
		return nil, err
	}
	names, readErr := fs.ReadDir(root.FS(), ".")
	err = errors.Join(readErr, root.Close())
	if err != nil || len(names) != 0 {
		return nil, errors.Join(ErrConflict, err)
	}
	if err := directory.CompareAndPublish(ctx, historyProjectionBindingFile, nil, bindBytes); err != nil {
		return nil, err
	}
	owners, err := openHistoryProjectionOwners(ctx, source, request.Directory)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, owners.close()) }()
	for index, step := range history.state.manifest.Steps[:stop] {
		if err := owners.apply(ctx, step, history.state.payloads[index], history.state, binding.Through, encryption); err != nil {
			return nil, err
		}
	}
	record, err := owners.snapshot(ctx, historySHA256(bindBytes), stop)
	if err != nil {
		return nil, err
	}
	if err := owners.close(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if err := directory.CompareAndPublish(ctx, historyProjectionRecordFile, nil, body); err != nil {
		return nil, err
	}
	return &IndependentHistoryProjection{state: &independentHistoryProjectionState{directory: request.Directory, binding: binding, bindBytes: bytes.Clone(bindBytes), record: record, bytes: bytes.Clone(body)}}, nil
}

func deriveHistoryProjectionBinding(source *RestoreSource, request HistoryProjectionRequest, history *VerifiedHistory) (historyProjectionBinding, []byte, int, error) {
	stop, err := catalogPrefixCount(history.state.manifest.Steps)
	if err != nil || history.state.manifest.Disposition != historyReplayComplete {
		return historyProjectionBinding{}, nil, 0, errors.Join(ErrConflict, err)
	}
	binding := historyProjectionBinding{Version: 1, BackupSHA256: source.ManifestDigest(), DeploymentID: source.DeploymentID(), History: request.History, Through: history.state.manifest.Interval.Through, Attestation: request.Attestation}
	var kvFinal historyKVAuthorityPayload
	var sqlFinal historySQLAuthorityPayload
	if _, err := decodeHistoryPayload(historyKVAuthorityFinal, history.state.payloads[stop], &kvFinal); err != nil || kvFinal.Version != 1 || !explicitHistoryMembers(history.state.payloads[stop], "expected_sha256") || kvFinal.ExpectedSHA256 != "" && !historyDigest(kvFinal.ExpectedSHA256) {
		return historyProjectionBinding{}, nil, 0, ErrConflict
	}
	if _, err := decodeHistoryPayload(historySQLAuthorityFinal, history.state.payloads[stop+1], &sqlFinal); err != nil || sqlFinal.Version != 1 || !explicitHistoryMembers(history.state.payloads[stop+1], "expected") || revision.ValidateSQLRecoveryPreimage(sqlFinal.Expected) != nil {
		return historyProjectionBinding{}, nil, 0, ErrConflict
	}
	binding.KVRevision, binding.SQLRevision = kvFinal.ExpectedSHA256, sqlFinal.Expected
	bindBytes, err := json.Marshal(binding, json.Deterministic(true))
	if err != nil {
		return historyProjectionBinding{}, nil, 0, err
	}
	return binding, bindBytes, stop, nil
}

func readCompletedHistoryProjection(ctx context.Context, directory *productfiles.Directory, request HistoryProjectionRequest, binding historyProjectionBinding, bindBytes []byte, stop int) (*IndependentHistoryProjection, bool, error) {
	prior, err := directory.ReadFile(historyProjectionBindingFile, 64<<10)
	if err == nil {
		if !bytes.Equal(prior, bindBytes) {
			return nil, true, ErrConflict
		}
		body, err := directory.ReadFile(historyProjectionRecordFile, 64<<10)
		if err != nil {
			return nil, true, errors.Join(ErrConflict, err)
		}
		var record historyProjectionRecord
		if json.Unmarshal(body, &record, json.RejectUnknownMembers(true)) != nil || record.Version != 1 || record.BindingSHA256 != historySHA256(bindBytes) || record.PrefixSteps != stop {
			return nil, true, ErrConflict
		}
		projected := &IndependentHistoryProjection{state: &independentHistoryProjectionState{directory: request.Directory, binding: binding, bindBytes: bytes.Clone(bindBytes), record: record, bytes: bytes.Clone(body)}}
		return projected, true, projected.checkImages(ctx)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, true, err
	}
	return nil, false, nil
}

// ProjectionComparison reports typed state equality while retaining all authority requirements.
// SQL witness and controls identify captured facts, not permission to adopt those facts.
type ProjectionComparison struct {
	ProjectionSHA256    string                            `json:"projection_sha256"`
	CapturedSHA256      string                            `json:"captured_sha256"`
	Through             time.Time                         `json:"through_utc"`
	SQL                 sqlstore.RelationalRecoveryCensus `json:"sql"`
	Restricted          bool                              `json:"restricted"`
	RequiresAuthority   bool                              `json:"requires_authority"`
	RequiresNativeClaim bool                              `json:"requires_native_claim"`
	RequiresCatalog     bool                              `json:"requires_catalog"`
	RequiresFiles       bool                              `json:"requires_files"`
	RequiresFencing     bool                              `json:"requires_fencing"`
}

// CompareCaptured checks a separately verified captured image without opening its live targets.
// Missing or different acknowledged domain state refuses. It does not validate native reopening authority.
func (p *IndependentHistoryProjection) CompareCaptured(ctx context.Context, captured *RestoreSource) (_ ProjectionComparison, resultErr error) {
	if ctx == nil || p == nil || p.state == nil || captured == nil || captured.DeploymentID() != p.state.binding.DeploymentID {
		return ProjectionComparison{}, ErrConflict
	}
	if err := p.checkImages(ctx); err != nil {
		return ProjectionComparison{}, err
	}
	if err := captured.checkCapturedManifest(ctx); err != nil {
		return ProjectionComparison{}, err
	}
	expected, err := OpenKVSnapshot(ctx, KVSnapshotPath(filepath.Join(p.state.directory, historyProjectionKVDirectory)), p.state.directory, p.state.record.KV)
	if err != nil {
		return ProjectionComparison{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, expected.Close()) }()
	actual, err := captured.OpenCapturedKV(ctx)
	if err != nil {
		return ProjectionComparison{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, actual.Close()) }()
	if err := p.compareCapturedKV(ctx, expected, actual); err != nil {
		return ProjectionComparison{}, err
	}
	view, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(captured.request.Directory, filepath.FromSlash(bundleSQLFile)), captured.manifest.SQL, p.state.directory)
	if err != nil {
		return ProjectionComparison{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, view.Close()) }()
	stamp, err := revision.CaptureSQLSnapshotRecovery(ctx, view)
	if err != nil || (stamp == nil) != (p.state.binding.SQLRevision == nil) || stamp != nil && *stamp != *p.state.binding.SQLRevision {
		return ProjectionComparison{}, errors.Join(ErrConflict, err)
	}
	census, err := view.RecoveryCensus(ctx)
	if err != nil || census.DomainSHA256 != p.state.record.SQLCensus.DomainSHA256 || census.DomainRows != p.state.record.SQLCensus.DomainRows || census.AuditHighWater != p.state.record.SQLCensus.AuditHighWater {
		return ProjectionComparison{}, errors.Join(ErrConflict, err)
	}
	if captured.manifest.Blobs != p.state.record.Blobs {
		return ProjectionComparison{}, ErrConflict
	}
	assets, err := blob.OpenSnapshot(ctx, filepath.Join(captured.request.Directory, bundleBlobFile), p.state.directory, captured.manifest.Blobs)
	if err != nil {
		return ProjectionComparison{}, err
	}
	if err := assets.Close(); err != nil {
		return ProjectionComparison{}, err
	}
	return ProjectionComparison{ProjectionSHA256: historySHA256(p.state.bytes), CapturedSHA256: captured.ManifestDigest(), Through: p.state.binding.Through, SQL: census,
		Restricted: true, RequiresAuthority: true, RequiresNativeClaim: true, RequiresCatalog: true, RequiresFiles: true, RequiresFencing: true}, nil
}

func (p *IndependentHistoryProjection) compareCapturedKV(ctx context.Context, expected, actual *KVSnapshotView) error {
	_, digest, err := revision.CaptureKVRecovery(ctx, actual)
	if err != nil || digest != p.state.binding.KVRevision {
		return errors.Join(ErrConflict, err)
	}
	original, _, err := revision.CaptureKVRecovery(ctx, expected)
	if err != nil {
		return err
	}
	wantCount := p.state.record.KV.Records
	if original != nil {
		wantCount--
	}
	var count int64
	err = actual.Enumerate(ctx, func(record storage.TransferRecord) error {
		if record.Key == revision.StorageKey {
			return nil // The revision owner checked its independent final preimage above.
		}
		other, err := expected.ReadCaptured(ctx, record.Key, storage.TransferMaxValueBytes)
		if err != nil || other.ExpiresAtMillis != record.ExpiresAtMillis || !bytes.Equal(other.Value, record.Value) {
			return errors.Join(ErrConflict, err)
		}
		count++
		return nil
	})
	if err != nil || count != wantCount {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func (p *IndependentHistoryProjection) checkImages(ctx context.Context) (resultErr error) {
	if ctx == nil || p == nil || p.state == nil || p.state.record.Version != 1 || p.state.record.BindingSHA256 != historySHA256(p.state.bindBytes) {
		return ErrConflict
	}
	binding, err := json.Marshal(p.state.binding, json.Deterministic(true))
	if err != nil || !bytes.Equal(binding, p.state.bindBytes) {
		return errors.Join(ErrConflict, err)
	}
	record, err := json.Marshal(p.state.record, json.Deterministic(true))
	if err != nil || !bytes.Equal(record, p.state.bytes) {
		return errors.Join(ErrConflict, err)
	}
	directory, err := productfiles.ExistingDirectory(p.state.directory)
	if err != nil {
		return err
	}
	root, err := directory.Open()
	if err != nil {
		return err
	}
	names, readErr := fs.ReadDir(root.FS(), ".")
	if err := errors.Join(readErr, root.Close()); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, entry := range names {
		switch entry.Name() {
		case historyProjectionBindingFile, historyProjectionRecordFile, "result-blobs.tar":
			if !entry.Type().IsRegular() {
				return ErrConflict
			}
		case historyProjectionKVDirectory, "result-sql", historyProjectionPublicationDirectory:
			if !entry.IsDir() {
				return ErrConflict
			}
		default:
			return ErrConflict
		}
		seen[entry.Name()] = true
	}
	for _, name := range []string{historyProjectionBindingFile, historyProjectionRecordFile, "result-blobs.tar", historyProjectionKVDirectory, "result-sql"} {
		if !seen[name] {
			return ErrConflict
		}
	}
	for name, expected := range map[string][]byte{historyProjectionBindingFile: p.state.bindBytes, historyProjectionRecordFile: p.state.bytes} {
		body, err := directory.ReadFile(name, 64<<10)
		if err != nil || !bytes.Equal(body, expected) {
			return errors.Join(ErrConflict, err)
		}
	}
	kv, err := OpenKVSnapshot(ctx, KVSnapshotPath(filepath.Join(p.state.directory, historyProjectionKVDirectory)), p.state.directory, p.state.record.KV)
	if err != nil {
		return err
	}
	if err := kv.Close(); err != nil {
		return err
	}
	sql, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(p.state.directory, "result-sql", "starport.db"), p.state.record.SQL, p.state.directory)
	if err != nil {
		return err
	}
	census, readErr := sql.RecoveryCensus(ctx)
	if err := errors.Join(readErr, sql.Close()); err != nil || census != p.state.record.SQLCensus {
		return errors.Join(ErrConflict, err)
	}
	assets, err := blob.OpenSnapshot(ctx, filepath.Join(p.state.directory, "result-blobs.tar"), p.state.directory, p.state.record.Blobs)
	if err != nil {
		return err
	}
	return assets.Close()
}
