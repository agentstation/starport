package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// HistoryAttestation records external facts accepted by an operator.
// Starport verifies their binding, not their truth or completeness.
type HistoryAttestation struct {
	Operator              string `json:"operator"`
	Reference             string `json:"reference"`
	WritersFenced         bool   `json:"writers_fenced"`
	AdmittedWorkAccounted bool   `json:"admitted_work_accounted"`
	CompleteInterval      bool   `json:"complete_interval"`
}

// HistoryAcceptanceRequest selects a private journal and explicit external attestations.
// The journal must already exist and remain available through recovery.
type HistoryAcceptanceRequest struct {
	Directory   string
	Attestation HistoryAttestation
}

type historyAcceptance struct {
	Version        int                `json:"version"`
	HistorySHA256  string             `json:"history_sha256"`
	BackupSHA256   string             `json:"backup_sha256"`
	PreparedSHA256 string             `json:"prepared_sha256"`
	TargetSHA256   string             `json:"target_sha256"`
	Operation      RestoreOperation   `json:"operation"`
	Attestation    HistoryAttestation `json:"attestation"`
}

type acceptedHistoryState struct {
	history   *verifiedHistoryState
	directory string
	body      []byte
	digest    string
	epoch     ImportedEpochRequest
	boundary  Record
}

// AcceptedHistory binds operator evidence to an immutable journal and closed SQL epoch.
// Acceptance permits no inference, worker dispatch, or import-barrier release.
// Every replay step still requires domain validation and native import guards.
type AcceptedHistory struct{ state *acceptedHistoryState }

// Format excludes private evidence and operator references from diagnostics.
func (AcceptedHistory) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private accepted recovery history>"))
}

// Digest identifies the exact retained acceptance bytes.
func (a *AcceptedHistory) Digest() string {
	if a == nil || a.state == nil {
		return ""
	}
	return a.state.digest
}

// AcceptImportedHistory retains external attestations before advancing the closed SQL epoch.
// Exact retries recheck both the private journal and the native SQL receipt.
// An operator can accept missing interval coverage only with a restricted disposition.
func (w *Witness) AcceptImportedHistory(ctx context.Context, source *RestoreSource, history *VerifiedHistory, request HistoryAcceptanceRequest) (*AcceptedHistory, error) {
	state, err := prepareHistoryAcceptance(source, history, request)
	if err != nil {
		return nil, err
	}
	if w == nil || w.db == nil || ctx == nil {
		return nil, ErrClosed
	}
	current, err := w.Current(ctx, state.boundary.DeploymentID)
	if err != nil || current != state.epoch.Prepared && current != state.boundary {
		return nil, errors.Join(ErrConflict, err)
	}
	if err := retainHistoryAcceptance(ctx, state, current == state.epoch.Prepared); err != nil {
		return nil, err
	}
	accepted := &AcceptedHistory{state: state}
	if err := accepted.check(ctx, w); err != nil {
		return nil, err
	}
	return accepted, nil
}

func prepareHistoryAcceptance(source *RestoreSource, history *VerifiedHistory, request HistoryAcceptanceRequest) (*acceptedHistoryState, error) {
	if source == nil || history == nil || history.state == nil || !filepath.IsAbs(request.Directory) || filepath.Clean(request.Directory) != request.Directory {
		return nil, ErrConflict
	}
	attestation := request.Attestation
	if !historyReference(attestation.Operator, 4096) || !historyReference(attestation.Reference, 4096) || !attestation.WritersFenced || !attestation.AdmittedWorkAccounted {
		return nil, ErrConflict
	}
	m := history.state.manifest
	if m.Disposition == "replay_complete" && !attestation.CompleteInterval {
		return nil, ErrConflict
	}
	identity, err := source.ImportIdentity(m.Operation)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(identity, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if err := m.validate(source, HistoryPackageRequest{Operation: m.Operation, TargetSHA256: m.TargetSHA256}, historySHA256(encoded)); err != nil {
		return nil, err
	}
	acceptance := historyAcceptance{Version: 1, HistorySHA256: history.Digest(), BackupSHA256: m.BackupSHA256, PreparedSHA256: m.PreparedSHA256, TargetSHA256: m.TargetSHA256, Operation: m.Operation, Attestation: attestation}
	body, err := json.Marshal(acceptance, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	digest := historySHA256(body)
	epoch := ImportedEpochRequest{Prepared: identity.Boundary, Snapshot: identity.SQLOriginal, Import: identity.SQL, Evidence: EpochEvidence{
		HighestEpoch: m.HighestEpoch.HighestEpoch,
		SourceSHA256: digest,
		Reference:    "accepted-history:" + history.Digest(),
		Operator:     attestation.Operator,
	}}
	boundary, _, err := epoch.next()
	if err != nil {
		return nil, err
	}
	return &acceptedHistoryState{history: history.state, directory: request.Directory, body: body, digest: digest, epoch: epoch, boundary: boundary}, nil
}

func retainHistoryAcceptance(ctx context.Context, state *acceptedHistoryState, allowCreate bool) error {
	directory, err := productfiles.ExistingDirectory(state.directory)
	if err != nil {
		return err
	}
	if err := directory.RecoverPublications(ctx); err != nil {
		return err
	}
	previous, err := directory.ReadFile("acceptance.json", historyManifestMaxBytes)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) && !allowCreate {
		return ErrConflict
	}
	if err == nil && !bytes.Equal(previous, state.body) {
		return ErrConflict
	}
	// Republishing the same bytes confirms durability after a lost publication reply.
	return directory.CompareAndPublish(ctx, "acceptance.json", previous, state.body)
}

func (a *AcceptedHistory) check(ctx context.Context, witness *Witness) error {
	if a == nil || a.state == nil || witness == nil || witness.db == nil || ctx == nil {
		return ErrClosed
	}
	directory, err := productfiles.ExistingDirectory(a.state.directory)
	if err != nil {
		return err
	}
	body, err := directory.ReadFile("acceptance.json", historyManifestMaxBytes)
	if err != nil || !bytes.Equal(body, a.state.body) {
		return errors.Join(ErrConflict, err)
	}
	boundary, err := witness.PrepareImportedEpoch(ctx, a.state.epoch)
	if err != nil || boundary != a.state.boundary {
		return errors.Join(ErrConflict, err)
	}
	return nil
}
