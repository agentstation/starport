package recovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

const closedFinalHistoryFile = "closed-final-history.json"
const closedFinalPreparationFile = "closed-final-prepared.json"
const closedFinalMaxAttempts = 16

// ClosedFinalRequest supplies the selected target and current external fence assertion.
// The composition root must derive TargetSHA256 from the configured native targets.
// Settings, canonical files, and catalog selection require separate owner checks.
type ClosedFinalRequest struct {
	TargetSHA256 string
	Attestation  HistoryAttestation
}

type closedFinalRecord struct {
	Version          int                        `json:"version"`
	BackupSHA256     string                     `json:"backup_sha256"`
	PreparedSHA256   string                     `json:"prepared_sha256"`
	AcceptanceSHA256 string                     `json:"acceptance_sha256"`
	HistorySHA256    string                     `json:"history_sha256"`
	RunSHA256        string                     `json:"run_sha256"`
	Replay           HistoryReplayReport        `json:"replay"`
	Attestation      HistoryAttestation         `json:"attestation"`
	Authority        revision.RecoveryAuthority `json:"authority"`
	GraphDirectory   string                     `json:"graph_directory"`
	GraphRequest     ImportedReferenceRequest   `json:"graph_request"`
	Graph            ImportedReferences         `json:"graph"`
}

// ClosedFinalReport describes checked history and a retained graph. Admission remains closed.
// This report cannot release import barriers or establish catalog or file authority.
type ClosedFinalReport struct {
	DecisionSHA256                   string              `json:"decision_sha256"`
	Replay                           HistoryReplayReport `json:"replay"`
	Graph                            ImportedReferences  `json:"graph"`
	Restricted                       bool                `json:"restricted"`
	RequiresSettings                 bool                `json:"requires_settings"`
	RequiresCanonicalFiles           bool                `json:"requires_canonical_files"`
	RequiresCatalogSelection         bool                `json:"requires_catalog_selection"`
	RequiresTransportTrust           bool                `json:"requires_transport_trust"`
	RequiresAdministratorCredentials bool                `json:"requires_administrator_credentials"`
}

// ClosedFinalHistory retains verified complete history without granting activation authority.
// Its private state binds the decision, graph images, exact native positions, and closed SQL witness.
type ClosedFinalHistory struct{ state *closedFinalState }

type closedFinalState struct {
	completed  *CompletedHistory
	record     closedFinalRecord
	body       []byte
	inspectors []CapturedKVInspector
}

// Format excludes private evidence and external operator references.
func (ClosedFinalHistory) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private closed final recovery history>"))
}

// Report returns diagnostics with every missing activation requirement explicit.
func (c *ClosedFinalHistory) Report() ClosedFinalReport {
	result := ClosedFinalReport{Restricted: true, RequiresSettings: true, RequiresCanonicalFiles: true,
		RequiresCatalogSelection: true, RequiresTransportTrust: true, RequiresAdministratorCredentials: true}
	if c != nil && c.state != nil && c.state.completed != nil {
		result.DecisionSHA256 = historySHA256(c.state.body)
		result.Replay = c.state.record.Replay
		result.Graph = c.state.record.Graph
	}
	return result
}

// FinalizeImportedHistory verifies complete replay and retains a checked final graph under closed admission.
// Exact retries verify the original images and time without capturing a replacement graph.
// Canonical files, settings, catalog selection, transport trust, and administrator credentials remain unchecked here.
func (w *Witness) FinalizeImportedHistory(ctx context.Context, completed *CompletedHistory, request ClosedFinalRequest, inspectors ...CapturedKVInspector) (*ClosedFinalHistory, error) {
	if err := checkClosedFinalInputs(ctx, w, completed, request); err != nil {
		return nil, err
	}
	runner := completed.runner
	if err := runner.directory.RecoverPublications(ctx); err != nil {
		return nil, err
	}
	body, err := runner.directory.ReadFile(closedFinalHistoryFile, historyManifestMaxBytes)
	var record closedFinalRecord
	if err == nil {
		if json.Unmarshal(body, &record, json.RejectUnknownMembers(true)) != nil {
			return nil, ErrConflict
		}
		canonical, err := json.Marshal(record, json.Deterministic(true))
		if err != nil || !bytes.Equal(canonical, body) {
			return nil, ErrConflict
		}
	} else if errors.Is(err, os.ErrNotExist) {
		preparation, err := retainClosedFinalPreparation(ctx, completed, request)
		if err != nil {
			return nil, err
		}
		record, err = completeClosedFinalCapture(ctx, w, completed, preparation, inspectors)
		if err != nil {
			return nil, err
		}
		if err := completed.check(ctx, w); err != nil {
			return nil, err
		}
		body, err = json.Marshal(record, json.Deterministic(true))
		if err != nil {
			return nil, err
		}
		if err := runner.directory.CompareAndPublish(ctx, closedFinalHistoryFile, nil, body); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	final := &ClosedFinalHistory{state: &closedFinalState{completed: completed, record: record, body: bytes.Clone(body), inspectors: append([]CapturedKVInspector(nil), inspectors...)}}
	if err := final.Check(ctx, w, request); err != nil {
		return nil, err
	}
	return final, nil
}

func checkClosedFinalInputs(ctx context.Context, w *Witness, completed *CompletedHistory, request ClosedFinalRequest) error {
	if err := completed.check(ctx, w); err != nil {
		return err
	}
	runner := completed.runner
	accepted := runner.accepted.state
	if !historyDigest(request.TargetSHA256) || request.TargetSHA256 != completed.report.TargetSHA256 ||
		request.Attestation != acceptedAttestation(runner) || !request.Attestation.CompleteInterval ||
		accepted.history.manifest.Disposition != historyReplayComplete || !completed.report.KVRotated || !completed.report.SQLRotated {
		return ErrConflict
	}
	return nil
}
func acceptedAttestation(runner *historyRunner) HistoryAttestation {
	var acceptance historyAcceptance
	// The caller has already checked the exact retained acceptance bytes.
	if json.Unmarshal(runner.accepted.state.body, &acceptance) != nil {
		return HistoryAttestation{}
	}
	return acceptance.Attestation
}
func newClosedFinalRecord(completed *CompletedHistory, request ClosedFinalRequest) closedFinalRecord {
	r := completed.runner
	m := r.accepted.state.history.manifest
	return closedFinalRecord{Version: 1, BackupSHA256: m.BackupSHA256, PreparedSHA256: m.PreparedSHA256,
		AcceptanceSHA256: r.accepted.Digest(), HistorySHA256: r.accepted.state.history.digest, RunSHA256: historySHA256(r.runBytes),
		Replay: completed.report, Attestation: request.Attestation, Authority: historyAcceptedAuthority(r.accepted),
		GraphDirectory: "final-graph-" + rand.Text(), GraphRequest: ImportedReferenceRequest{Boundary: completed.report.Boundary,
			CapturedAt: time.Now().UTC(), KVClaim: bytes.Clone(r.identity.KVClaim), KVPosition: completed.report.Positions.KV,
			SQLOriginal: r.identity.SQLOriginal, SQLIdentity: r.identity.SQL, SQLPosition: completed.report.Positions.SQL,
			BlobOperation: r.identity.ComponentOperation, BlobPosition: completed.report.Positions.Blobs, BlobOriginal: r.identity.BlobOriginal}}
}

// Check verifies the retained closed decision against its original native targets.
// The caller must recheck configured target identity and maintain external fencing.
// Success grants no authority to admit requests or release an import barrier.
func (c *ClosedFinalHistory) Check(ctx context.Context, w *Witness, request ClosedFinalRequest) error {
	if c == nil || c.state == nil || c.state.completed == nil {
		return ErrConflict
	}
	if err := checkClosedFinalInputs(ctx, w, c.state.completed, request); err != nil {
		return err
	}
	r := c.state.completed.runner
	body, err := r.directory.ReadFile(closedFinalHistoryFile, historyManifestMaxBytes)
	if err != nil || !bytes.Equal(body, c.state.body) {
		return errors.Join(ErrConflict, err)
	}
	if err := c.validateRecord(); err != nil {
		return err
	}
	graph := filepath.Join(r.accepted.state.directory, c.state.record.GraphDirectory)
	if _, err := productfiles.ExistingDirectory(graph); err != nil {
		return err
	}
	report, err := inspectImportedCopies(ctx, filepath.Join(graph, "kv"), filepath.Join(graph, "sql"), filepath.Join(graph, "blobs.tar"),
		r.request.ScratchDirectory, c.state.record.GraphRequest, c.state.record.Graph, r.targets.Encryption, closedFinalInspectors(c.state.record.Authority, c.state.inspectors)...)
	if err != nil {
		return err
	}
	reportBytes, err := json.Marshal(report, json.Deterministic(true))
	if err != nil {
		return err
	}
	retainedBytes, err := json.Marshal(c.state.record.Graph.References, json.Deterministic(true))
	if err != nil || !bytes.Equal(reportBytes, retainedBytes) {
		return ErrConflict
	}
	if err := checkClosedFinalKVRevision(ctx, c.state.completed, c.state.record.Authority); err != nil {
		return err
	}
	if err := checkClosedFinalSQLRevision(ctx, w, c.state.record.Authority); err != nil {
		return err
	}
	return c.state.completed.check(ctx, w)
}
func (c *ClosedFinalHistory) validateRecord() error {
	preparation, err := readClosedFinalPreparation(c.state.completed)
	if err != nil {
		return err
	}
	if err := validateClosedFinalRecord(c.state.record, preparation); err != nil {
		return err
	}
	actual, err := json.Marshal(c.state.record, json.Deterministic(true))
	if err != nil || !bytes.Equal(actual, c.state.body) {
		return ErrConflict
	}
	return nil
}
func validFinalGraphName(name string) bool {
	const prefix = "final-graph-"
	if len(name) != len(prefix)+26 || name[:len(prefix)] != prefix {
		return false
	}
	for _, char := range name[len(prefix):] {
		if char < 'A' || char > 'Z' {
			if char < '2' || char > '7' {
				return false
			}
		}
	}
	return true
}
func closedFinalInspectors(authority revision.RecoveryAuthority, extra []CapturedKVInspector) []CapturedKVInspector {
	result := append([]CapturedKVInspector(nil), extra...)
	return append(result, func(ctx context.Context, view *KVSnapshotView, _ Record) error {
		stamp, _, err := revision.CaptureKVRecovery(ctx, view)
		if err != nil || stamp == nil || stamp.Epoch != authority.Epoch || stamp.Sequence != 1 {
			return errors.Join(ErrConflict, err)
		}
		return nil
	})
}
func checkClosedFinalSQLRevision(ctx context.Context, w *Witness, authority revision.RecoveryAuthority) (resultErr error) {
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	stamp, err := revision.CaptureSQLRecovery(ctx, w.db, conn)
	if err != nil || stamp == nil || stamp.Epoch != authority.Epoch || stamp.Sequence != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// Bridge the closed replay contract to the legacy zero-position inspection methods.
type closedFinalBlobInspector struct{ HistoryBlobTarget }

func (b closedFinalBlobInspector) SnapshotImport(ctx context.Context, path, operation string, original blob.Snapshot) (blob.Snapshot, error) {
	return b.SnapshotImportAt(ctx, path, operation, original, blob.ImportReplayPosition{})
}
func (b closedFinalBlobInspector) CheckImport(ctx context.Context, operation string, original blob.Snapshot) error {
	return b.CheckImportPosition(ctx, operation, original, blob.ImportReplayPosition{})
}

// Read the native revision through the owner decoder under the exact import position.
func checkClosedFinalKVRevision(ctx context.Context, completed *CompletedHistory, authority revision.RecoveryAuthority) error {
	r := completed.runner
	reader := closedFinalRevisionReader{}
	err := r.targets.KV.InspectImport(ctx, r.identity.KVClaim, completed.report.Positions.KV, func(record storage.TransferRecord) error {
		if record.Key != revision.StorageKey {
			return nil
		}
		if reader.found && (!bytes.Equal(reader.record.Value, record.Value) || reader.record.ExpiresAtMillis != record.ExpiresAtMillis) {
			return ErrConflict
		}
		reader.record = record
		reader.record.Value = bytes.Clone(record.Value)
		reader.found = true
		return nil
	})
	if err != nil {
		return err
	}
	stamp, _, err := revision.CaptureKVRecovery(ctx, reader)
	if err != nil || stamp == nil || stamp.Epoch != authority.Epoch || stamp.Sequence != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

type closedFinalRevisionReader struct {
	record storage.TransferRecord
	found  bool
}

func (r closedFinalRevisionReader) ReadCaptured(ctx context.Context, key string, limit int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	if !r.found || key != revision.StorageKey {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	if len(r.record.Value) > limit {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return r.record, nil
}

func retainClosedFinalPreparation(ctx context.Context, completed *CompletedHistory, request ClosedFinalRequest) (closedFinalRecord, error) {
	r := completed.runner
	_, err := r.directory.ReadFile(closedFinalPreparationFile, historyManifestMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		if err := checkClosedFinalPreparationAbsent(r); err != nil {
			return closedFinalRecord{}, err
		}
		record := newClosedFinalRecord(completed, request)
		body, err := json.Marshal(record, json.Deterministic(true))
		if err != nil {
			return closedFinalRecord{}, err
		}
		if err := r.directory.CompareAndPublish(ctx, closedFinalPreparationFile, nil, body); err != nil {
			return closedFinalRecord{}, err
		}
	} else if err != nil {
		return closedFinalRecord{}, err
	}
	return readClosedFinalPreparation(completed)
}
func readClosedFinalPreparation(completed *CompletedHistory) (closedFinalRecord, error) {
	r := completed.runner
	body, err := r.directory.ReadFile(closedFinalPreparationFile, historyManifestMaxBytes)
	if err != nil {
		return closedFinalRecord{}, err
	}
	var record closedFinalRecord
	if json.Unmarshal(body, &record, json.RejectUnknownMembers(true)) != nil {
		return closedFinalRecord{}, ErrConflict
	}
	expected := newClosedFinalRecord(completed, ClosedFinalRequest{Attestation: acceptedAttestation(r)})
	expected.GraphDirectory = record.GraphDirectory
	expected.GraphRequest.CapturedAt = record.GraphRequest.CapturedAt
	canonical, err := json.Marshal(expected, json.Deterministic(true))
	if err != nil || !bytes.Equal(body, canonical) || !validFinalGraphName(record.GraphDirectory) || record.GraphRequest.CapturedAt.IsZero() || record.GraphRequest.CapturedAt.Before(r.run.ValidatedAt) {
		return closedFinalRecord{}, ErrConflict
	}
	return record, nil
}
func validateClosedFinalRecord(record, preparation closedFinalRecord) error {
	expected := preparation
	expected.GraphDirectory = record.GraphDirectory
	expected.Graph = record.Graph
	actual, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return err
	}
	wanted, err := json.Marshal(expected, json.Deterministic(true))
	if err != nil || !bytes.Equal(actual, wanted) {
		return ErrConflict
	}
	parent, attempt := filepath.Split(record.GraphDirectory)
	if strings.TrimSuffix(parent, string(filepath.Separator)) != preparation.GraphDirectory || !validClosedFinalAttempt(attempt) {
		return ErrConflict
	}
	requestBytes, err := json.Marshal(record.GraphRequest)
	if err != nil || historySHA256(requestBytes) != record.Graph.RequestSHA256 {
		return ErrConflict
	}
	return nil
}
func validClosedFinalAttempt(name string) bool {
	for i := 1; i <= closedFinalMaxAttempts; i++ {
		if name == fmt.Sprintf("attempt-%06d", i) {
			return true
		}
	}
	return false
}
func completeClosedFinalCapture(ctx context.Context, w *Witness, completed *CompletedHistory, preparation closedFinalRecord, inspectors []CapturedKVInspector) (closedFinalRecord, error) {
	r := completed.runner
	namespace, err := r.directory.ExistingChild(preparation.GraphDirectory)
	if errors.Is(err, os.ErrNotExist) {
		namespace, err = r.directory.CreateChild(preparation.GraphDirectory)
	}
	if err != nil {
		return closedFinalRecord{}, err
	}
	if err := namespace.RecoverPublications(ctx); err != nil {
		return closedFinalRecord{}, err
	}
	for i := 1; i <= closedFinalMaxAttempts; i++ {
		name := fmt.Sprintf("attempt-%06d", i)
		body, err := namespace.ReadFile(name+".json", historyManifestMaxBytes)
		if err == nil {
			var record closedFinalRecord
			if json.Unmarshal(body, &record, json.RejectUnknownMembers(true)) != nil || validateClosedFinalRecord(record, preparation) != nil || record.GraphDirectory != filepath.Join(preparation.GraphDirectory, name) {
				return closedFinalRecord{}, ErrConflict
			}
			canonical, err := json.Marshal(record, json.Deterministic(true))
			if err != nil || !bytes.Equal(body, canonical) {
				return closedFinalRecord{}, ErrConflict
			}
			return record, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return closedFinalRecord{}, err
		}
		_, err = namespace.ExistingChild(name)
		if err == nil {
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return closedFinalRecord{}, err
		}
		if err := completed.check(ctx, w); err != nil {
			return closedFinalRecord{}, err
		}
		if err := checkClosedFinalKVRevision(ctx, completed, preparation.Authority); err != nil {
			return closedFinalRecord{}, err
		}
		if err := checkClosedFinalSQLRevision(ctx, w, preparation.Authority); err != nil {
			return closedFinalRecord{}, err
		}
		record := preparation
		record.GraphDirectory = filepath.Join(preparation.GraphDirectory, name)
		graph, err := InspectImportedReferences(ctx, ImportedReferenceSources{KV: r.targets.KV, SQL: w.db, Blobs: closedFinalBlobInspector{r.targets.Blobs}},
			record.GraphRequest, filepath.Join(r.accepted.state.directory, record.GraphDirectory), r.request.ScratchDirectory, r.targets.Encryption,
			closedFinalInspectors(record.Authority, inspectors)...)
		if err != nil {
			return closedFinalRecord{}, err
		}
		record.Graph = graph
		if err := checkClosedFinalKVRevision(ctx, completed, record.Authority); err != nil {
			return closedFinalRecord{}, err
		}
		if err := checkClosedFinalSQLRevision(ctx, w, record.Authority); err != nil {
			return closedFinalRecord{}, err
		}
		if err := completed.check(ctx, w); err != nil {
			return closedFinalRecord{}, err
		}
		body, err = json.Marshal(record, json.Deterministic(true))
		if err != nil {
			return closedFinalRecord{}, err
		}
		if err := namespace.CompareAndPublish(ctx, name+".json", nil, body); err != nil {
			return closedFinalRecord{}, err
		}
		return record, nil
	}
	return closedFinalRecord{}, ErrConflict
}

func checkClosedFinalPreparationAbsent(r *historyRunner) error {
	root, err := r.directory.Open()
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	err = errors.Join(err, root.Close())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == closedFinalHistoryFile || strings.HasPrefix(entry.Name(), "final-graph-") {
			return ErrConflict
		}
	}
	return nil
}
