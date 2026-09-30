package recovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// closedAdoptionMode names the first populated adoption delivery in identities and journals.
const closedAdoptionMode = "closed_adoption_zero_prefix"

var (
	// ErrRestoredWitness refuses a captured SQL witness at or below the prior approval epoch.
	// A lower epoch shows a restored SQL database.
	ErrRestoredWitness = fmt.Errorf("%w: captured SQL witness does not follow the prior approval", ErrConflict)
	// ErrEpochConflict refuses independent epoch evidence that disagrees with the prior approval.
	ErrEpochConflict = fmt.Errorf("%w: independent recovery epochs differ", ErrConflict)
	// ErrAdoptionPrefix refuses populated adoption of a history with a step before the final authorization steps.
	ErrAdoptionPrefix = fmt.Errorf("%w: populated adoption requires a history without prefix steps", ErrConflict)
)

// PopulatedTargets supplies the populated native owners of one fenced deployment.
// Every external writer must stay fenced from the control observation through activation.
type PopulatedTargets struct {
	KV    storage.PopulatedImportClaimer
	SQL   *sqlstore.DB
	Blobs blob.PopulatedImportClaimer
}

// PopulatedControls holds the native control preimages observed before the first claim.
// The caller retains them independently. A claim retry must reuse the same observation.
type PopulatedControls struct {
	KV    storage.ImportControls `json:"kv"`
	Blobs blob.PopulatedControls `json:"blobs"`
}

// PopulatedClaimRequest binds a populated claim to the operator's prior approval and retained controls.
// ScratchDirectory must be an existing clean absolute private directory.
type PopulatedClaimRequest struct {
	PriorApproval    Record
	Controls         PopulatedControls
	ScratchDirectory string
}

// ObservePopulatedControls reads the native control preimages without a change.
// A target that already holds a claim refuses. The caller retains the result before any claim.
func ObservePopulatedControls(ctx context.Context, targets PopulatedTargets) (PopulatedControls, error) {
	if ctx == nil || targets.KV == nil || targets.Blobs == nil {
		return PopulatedControls{}, ErrConflict
	}
	kv, err := targets.KV.ObserveImportControls(ctx)
	if err != nil {
		return PopulatedControls{}, err
	}
	blobs, err := targets.Blobs.ObservePopulatedControls(ctx)
	if err != nil {
		return PopulatedControls{}, err
	}
	return PopulatedControls{KV: kv, Blobs: blobs}, nil
}

// AdoptionIdentity derives the populated claims of the captured deployment C.
// The history must bind C.ImportIdentity for its operation and hold only the two final authorization steps.
// The captured SQL witness must equal the prior approval after Close.
// The claims differ from the empty-target import claims of the same capture.
func (s *RestoreSource) AdoptionIdentity(history *VerifiedHistory, prior Record) (PreparedImportIdentity, error) {
	if s == nil || s.manifest.Format != bundleFormat || history == nil || history.state == nil {
		return PreparedImportIdentity{}, ErrConflict
	}
	m := history.state.manifest
	imported, err := s.ImportIdentity(m.Operation)
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	encoded, err := json.Marshal(imported, json.Deterministic(true))
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	if err := m.validate(s, HistoryPackageRequest{Operation: m.Operation, TargetSHA256: m.TargetSHA256}, historySHA256(encoded)); err != nil {
		return PreparedImportIdentity{}, err
	}
	if err := checkAdoptionSteps(m.Steps); err != nil {
		return PreparedImportIdentity{}, err
	}
	captured := s.manifest.Request.Boundary
	if err := bindPriorApproval(captured, prior); err != nil {
		return PreparedImportIdentity{}, err
	}
	identity, err := json.Marshal(struct {
		Version                            int
		Mode, Operation, Manifest, History string
		FencingEvidence                    string `json:",omitempty"`
	}{1, closedAdoptionMode, m.Operation.ID, s.request.ManifestSHA256, history.Digest(), m.Operation.FencingEvidence})
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	digest := sha256.Sum256(identity)
	component := hex.EncodeToString(digest[:])
	relational, boundary, err := sqlAdoptionIdentity(component, s.request.ManifestSHA256, captured, prior)
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	claim, err := json.Marshal(kvImportClaim{Version: 1, OperationID: component, Snapshot: s.manifest.KV})
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	return PreparedImportIdentity{ComponentOperation: component, Boundary: boundary, KVClaim: claim,
		KVOriginal: s.manifest.KV, SQL: relational, SQLOriginal: s.manifest.SQL, BlobOriginal: s.manifest.Blobs}, nil
}

// checkAdoptionSteps permits only the final KV and SQL authorization steps.
func checkAdoptionSteps(steps []historyStep) error {
	count, err := catalogPrefixCount(steps)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrAdoptionPrefix
	}
	return nil
}

// bindPriorApproval requires the captured witness to equal the prior approval after Close.
func bindPriorApproval(captured, prior Record) error {
	if !prior.Open || validDeployment(prior.DeploymentID) != nil || prior.Epoch <= 0 || prior.Epoch >= math.MaxInt64-1 ||
		strings.TrimSpace(prior.BackendID) == "" || len(prior.BackendID) > 256 || strings.TrimSpace(prior.Evidence) == "" || len(prior.Evidence) > 4096 ||
		captured.Open || captured.DeploymentID != prior.DeploymentID {
		return ErrConflict
	}
	if captured.Epoch <= prior.Epoch {
		return ErrRestoredWitness
	}
	closed := prior
	closed.Epoch++
	closed.Open = false
	if captured != closed {
		return ErrEpochConflict
	}
	return nil
}

func sqlAdoptionIdentity(operation, manifest string, captured, prior Record) (sqlstore.RelationalImportIdentity, Record, error) {
	if captured.Epoch >= math.MaxInt64-1 {
		return sqlstore.RelationalImportIdentity{}, Record{}, ErrConflict
	}
	policy, err := json.Marshal(struct {
		Version   string
		Operation string
		Manifest  string
		Captured  Record
		Prior     Record
	}{"sql-adoption-closed-v1", operation, manifest, captured, prior})
	if err != nil {
		return sqlstore.RelationalImportIdentity{}, Record{}, err
	}
	digest := sha256.Sum256(policy)
	policyID := hex.EncodeToString(digest[:])
	expected := Record{DeploymentID: captured.DeploymentID, Epoch: captured.Epoch + 1, BackendID: captured.BackendID, Evidence: "adoption-prepared:" + policyID}
	return sqlstore.RelationalImportIdentity{OperationID: operation, RestrictionID: policyID}, expected, nil
}

// checkPriorAuthority requires the captured KV authority record to hold the prior approval.
func checkPriorAuthority(ctx context.Context, view *KVSnapshotView, prior Record) error {
	record, err := view.ReadCaptured(ctx, authorityKey, 8192)
	if errors.Is(err, storage.ErrNotFound) {
		return ErrEpochConflict
	}
	if err != nil {
		return err
	}
	var authority authorityRecord
	if record.ExpiresAtMillis != 0 || json.Unmarshal(record.Value, &authority, json.RejectUnknownMembers(true)) != nil ||
		authority.Version != 1 || authority.Approval != prior || strings.TrimSpace(authority.OperationID) == "" {
		return ErrEpochConflict
	}
	return nil
}

// ClaimPopulated places the adoption claims on the populated targets of the captured deployment C.
// Each owner first checks its live state against C exactly and copies no records.
// The SQL claim moves the captured closed witness to the adoption-prepared record in the same transaction.
// An exact retry with the same controls continues the claim. A retry after acceptance refuses.
// Success leaves every import barrier in place and grants no admission.
func (s *RestoreSource) ClaimPopulated(ctx context.Context, history *VerifiedHistory, targets PopulatedTargets, request PopulatedClaimRequest) (_ PreparedImportIdentity, resultErr error) {
	if ctx == nil || targets.KV == nil || targets.SQL == nil || targets.Blobs == nil ||
		!filepath.IsAbs(request.ScratchDirectory) || filepath.Clean(request.ScratchDirectory) != request.ScratchDirectory {
		return PreparedImportIdentity{}, ErrConflict
	}
	if _, err := productfiles.ExistingDirectory(request.ScratchDirectory); err != nil {
		return PreparedImportIdentity{}, err
	}
	identity, err := s.AdoptionIdentity(history, request.PriorApproval)
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	witness, err := New(targets.SQL)
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	view, err := s.OpenCapturedKV(ctx)
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, view.Close()) }()
	if err := checkPriorAuthority(ctx, view, request.PriorApproval); err != nil {
		return PreparedImportIdentity{}, err
	}
	census, err := s.capturedRecoveryCensus(ctx, request.ScratchDirectory)
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	captured := s.manifest.Request.Boundary
	err = targets.SQL.ClaimPopulatedRelational(ctx, identity.SQLOriginal, census, request.ScratchDirectory, identity.SQL, func(ctx context.Context, conn *sql.Conn) error {
		if err := witness.checkLockedClosedBoundary(ctx, conn, captured); err != nil {
			return err
		}
		_, err := witness.replaceWith(ctx, conn, captured, identity.Boundary)
		return err
	})
	if err != nil {
		return PreparedImportIdentity{}, err
	}
	if err := targets.KV.ClaimPopulated(ctx, identity.KVClaim, request.Controls.KV, view); err != nil {
		return PreparedImportIdentity{}, err
	}
	blobs := filepath.Join(s.request.Directory, bundleBlobFile)
	if err := targets.Blobs.ClaimPopulated(ctx, blobs, request.ScratchDirectory, identity.ComponentOperation, identity.BlobOriginal, request.Controls.Blobs); err != nil {
		return PreparedImportIdentity{}, err
	}
	current, err := witness.Current(ctx, identity.Boundary.DeploymentID)
	if err != nil || current != identity.Boundary {
		return PreparedImportIdentity{}, errors.Join(ErrConflict, err)
	}
	if err := targets.SQL.CheckImportBarrier(ctx); !errors.Is(err, sqlstore.ErrImportRestricted) {
		return PreparedImportIdentity{}, errors.Join(ErrConflict, err)
	}
	return identity, nil
}

// capturedRecoveryCensus reads the relational census from the captured SQL image, not from a live target.
func (s *RestoreSource) capturedRecoveryCensus(ctx context.Context, scratch string) (_ sqlstore.RelationalRecoveryCensus, resultErr error) {
	view, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(s.request.Directory, bundleSQLFile), s.manifest.SQL, scratch)
	if err != nil {
		return sqlstore.RelationalRecoveryCensus{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, view.Close()) }()
	return view.RecoveryCensus(ctx)
}
