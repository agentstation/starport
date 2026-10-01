package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
)

const recoveryOperatorInputsFile = "operator-inputs.json"

// RecoveryOperatorInputsRequest binds existing target inputs to one fenced recovery operation.
// The caller retains external fencing and supplies opened native owner targets.
type RecoveryOperatorInputsRequest struct {
	Operation            recovery.RestoreOperation
	JournalDirectory     string
	ExpectedTargetSHA256 string
	ValkeyIncarnation    string
}
type recoveryOperatorInputRecord struct {
	Version         int                       `json:"version"`
	Operation       recovery.RestoreOperation `json:"operation"`
	JournalIdentity string                    `json:"journal_identity"`
	TargetSHA256    string                    `json:"target_sha256"`
	Inputs          jsontext.Value            `json:"inputs"`
}

// VerifiedOperatorInputs retains checked settings and selected local trust and administrator inputs.
// It does not release import barriers, grant catalog permission, or start a gateway.
type VerifiedOperatorInputs struct{ state *verifiedOperatorInputState }
type verifiedOperatorInputState struct {
	request   RecoveryOperatorInputsRequest
	directory *productfiles.Directory
	body      []byte
	files     int
}

// Format excludes private paths, operator references, and secret-derived digests.
func (VerifiedOperatorInputs) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private recovery operator inputs>"))
}

// RecoveryOperatorInputsReport contains diagnostics without activation authority.
type RecoveryOperatorInputsReport struct {
	DecisionSHA256 string `json:"decision_sha256"`
	SelectedFiles  int    `json:"selected_files"`
	Restricted     bool   `json:"restricted"`
}

// Report returns checked input counts. Admission remains closed.
func (v *VerifiedOperatorInputs) Report() RecoveryOperatorInputsReport {
	result := RecoveryOperatorInputsReport{Restricted: true}
	if v != nil && v.state != nil {
		digest := sha256.Sum256(v.state.body)
		result.DecisionSHA256 = hex.EncodeToString(digest[:])
		result.SelectedFiles = v.state.files
	}
	return result
}

// VerifyRecoveryOperatorInputs retains an immutable decision from the configured native targets.
// Exact retries recheck current selections. Captured backup configuration never replaces target inputs.
func VerifyRecoveryOperatorInputs(ctx context.Context, cfg *config.Config, db *sqlstore.DB, blobs blob.RestoreTarget, request RecoveryOperatorInputsRequest) (*VerifiedOperatorInputs, error) {
	if err := validateOperatorInputRequest(ctx, request); err != nil {
		return nil, err
	}
	directory, err := productfiles.ExistingDirectory(request.JournalDirectory)
	if err != nil {
		return nil, err
	}
	record, files, err := configuredOperatorInputRecord(ctx, cfg, db, blobs, request, directory)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if err := directory.RecoverPublications(ctx); err != nil {
		return nil, err
	}
	retained, err := directory.ReadFile(recoveryOperatorInputsFile, 8<<20)
	switch {
	case err == nil:
		if !bytes.Equal(retained, body) {
			return nil, recovery.ErrConflict
		}
	case errors.Is(err, os.ErrNotExist):
		if err := directory.CompareAndPublish(ctx, recoveryOperatorInputsFile, nil, body); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	verified := &VerifiedOperatorInputs{state: &verifiedOperatorInputState{request: request, directory: directory, body: bytes.Clone(body), files: files}}
	if err := verified.Check(ctx, cfg, db, blobs, request); err != nil {
		return nil, err
	}
	return verified, nil
}

// Check rechecks owner inputs and the exact retained decision against opened native targets.
// The activation coordinator must call it under the original external fence.
func (v *VerifiedOperatorInputs) Check(ctx context.Context, cfg *config.Config, db *sqlstore.DB, blobs blob.RestoreTarget, request RecoveryOperatorInputsRequest) error {
	if v == nil || v.state == nil || request != v.state.request {
		return recovery.ErrConflict
	}
	if err := validateOperatorInputRequest(ctx, request); err != nil {
		return err
	}
	if err := v.state.directory.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	record, _, err := configuredOperatorInputRecord(ctx, cfg, db, blobs, request, v.state.directory)
	if err != nil {
		return err
	}
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil || !bytes.Equal(body, v.state.body) {
		return recovery.ErrConflict
	}
	retained, err := v.state.directory.ReadFile(recoveryOperatorInputsFile, 8<<20)
	if err != nil || !bytes.Equal(retained, v.state.body) {
		return recovery.ErrConflict
	}
	return nil
}

func validateOperatorInputRequest(ctx context.Context, request RecoveryOperatorInputsRequest) error {
	if ctx == nil {
		return errors.New("operator input validation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(request.Operation.FencingEvidence) == "" {
		return errors.New("operator input validation requires external fencing evidence")
	}
	if err := request.Operation.Validate(); err != nil {
		return err
	}
	if len(request.ExpectedTargetSHA256) != 64 {
		return recovery.ErrConflict
	}
	if _, err := hex.DecodeString(request.ExpectedTargetSHA256); err != nil {
		return recovery.ErrConflict
	}
	return nil
}
func configuredOperatorInputRecord(ctx context.Context, cfg *config.Config, db *sqlstore.DB, blobs blob.RestoreTarget, request RecoveryOperatorInputsRequest, directory *productfiles.Directory) (recoveryOperatorInputRecord, int, error) {
	var record recoveryOperatorInputRecord
	if cfg == nil || db == nil || blobs == nil {
		return record, 0, errors.New("operator input validation requires configured native targets")
	}
	if err := checkOperatorInputJournal(cfg, request.JournalDirectory); err != nil {
		return record, 0, err
	}
	target, err := configuredRecoveryTarget(ctx, cfg, db, blobs, request.ValkeyIncarnation)
	if err != nil || target != request.ExpectedTargetSHA256 {
		return record, 0, recovery.ErrConflict
	}
	identity, err := directory.Identity()
	if err != nil {
		return record, 0, err
	}
	selections, err := cfg.InspectRecoverySelection(ctx)
	if err != nil {
		return record, 0, err
	}
	after, err := configuredRecoveryTarget(ctx, cfg, db, blobs, request.ValkeyIncarnation)
	if err != nil || after != target {
		return record, 0, recovery.ErrConflict
	}
	return recoveryOperatorInputRecord{Version: 1, Operation: request.Operation, JournalIdentity: identity, TargetSHA256: target, Inputs: selections.PrivateEvidence()}, selections.FileCount(), nil
}

func checkOperatorInputJournal(cfg *config.Config, path string) error {
	manifest, err := cfg.FileManifest("")
	if err != nil {
		return err
	}
	destination := filepath.Join(path, recoveryOperatorInputsFile)
	for _, entry := range manifest.Files {
		if entry.Location.Path == "" || entry.Availability != "available" && entry.ID != "configuration" {
			continue
		}
		switch entry.Kind {
		case "file":
			if err := recovery.CheckRestoreDestinations(destination, destination, entry.Location.Path); err != nil {
				return errors.New("operator input journal overlaps a canonical file")
			}
		case "tree":
			if err := recovery.CheckRestoreDestinations(path, path, entry.Location.Path); err != nil {
				return errors.New("operator input journal overlaps a canonical tree")
			}
		}
	}
	return nil
}

// Record exports original checked owner selections for the immutable activation decision.
// A supplied record or digest cannot replace current native owner checks.
func (v *VerifiedOperatorInputs) Record() ([]byte, error) {
	if v == nil || v.state == nil || len(v.state.body) == 0 || len(v.state.body) > 8<<20 {
		return nil, recovery.ErrConflict
	}
	return bytes.Clone(v.state.body), nil
}
