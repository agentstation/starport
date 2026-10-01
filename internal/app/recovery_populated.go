package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
)

const (
	populatedRecoveryPreparation = "populated.json"
	populatedRecoveryMaxBytes    = 1 << 20
)

// ErrCatalogDirectoryOccupied refuses populated adoption into a catalog state directory that a gateway owns.
var ErrCatalogDirectoryOccupied = fmt.Errorf("%w: populated adoption requires a new empty catalog state directory", recovery.ErrConflict)

// PopulatedRecoveryRequest binds one fenced populated deployment to its capture C, the history H, and the prior approval.
// Activation holds the original activation inputs. Its backup is C, and H binds C.ImportIdentity(operation).
// The operator retains PriorApproval, the last open witness record, independently of the deployment.
// PreparationDirectory is an existing private directory that retains the prepared record.
// It is separate from the activation directory, because the activation journal admits only its own records.
type PopulatedRecoveryRequest struct {
	Activation           RecoveryActivationRequest
	PriorApproval        recovery.Record
	PreparationDirectory string
	// ExpectedPreparedSHA256 must identify the retained prepared record on activation and inspection.
	ExpectedPreparedSHA256 string
}

// PopulatedRecoveryPreparation reports the retained prepared record. It grants no claim or permission.
type PopulatedRecoveryPreparation struct {
	PreparedSHA256 string `json:"prepared_sha256"`
	NextAction     string `json:"next_action"`
}

type populatedPreparationRecord struct {
	Version  int                        `json:"version"`
	Request  PopulatedRecoveryRequest   `json:"request"`
	Controls recovery.PopulatedControls `json:"controls"`
}

// Original removes the caller retry selectors from the immutable prepared inputs.
func (r PopulatedRecoveryRequest) Original() PopulatedRecoveryRequest {
	r.Activation = r.Activation.Original()
	r.ExpectedPreparedSHA256 = ""
	return r
}

// Validate checks the activation inputs, the prior approval shape, and each retry selector.
func (r PopulatedRecoveryRequest) Validate() error {
	if r.Activation.Validate() != nil || !r.Activation.PreserveTargetWorkspace || r.Activation.History.ValkeyIncarnation == "" || r.Activation.History.ScratchDirectory == "" {
		return recovery.ErrConflict
	}
	prior := r.PriorApproval
	if prior.DeploymentID == "" || prior.Epoch <= 0 || !prior.Open || prior.BackendID == "" || prior.Evidence == "" {
		return recovery.ErrConflict
	}
	if r.ExpectedPreparedSHA256 != "" && !populatedRecordDigest(r.ExpectedPreparedSHA256) {
		return recovery.ErrConflict
	}
	activation := r.Activation
	for _, path := range []string{activation.ActivationDirectory, activation.Prepare.Directory, activation.Prepare.FilesDirectory, activation.History.HistoryDirectory, activation.History.JournalDirectory, activation.History.ScratchDirectory} {
		if recovery.CheckRestoreDestinations(r.PreparationDirectory, r.PreparationDirectory, path) != nil {
			return recovery.ErrConflict
		}
	}
	return nil
}

// Format excludes operator evidence, the prior approval, and private source paths.
func (PopulatedRecoveryRequest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private populated recovery request>"))
}

// checkPopulatedCatalogDirectory requires a new catalog state directory before the first native claim.
// The live directory holds the selected materialization of the prior approval, and topology preparation refuses it.
// Adoption therefore prepares catalog identity in a directory that the operator selects for adoption and the fresh gateway.
func checkPopulatedCatalogDirectory(ctx context.Context, cfg *config.Config) error {
	target, _, err := catalogSettings(cfg).RecoveryTopologyTarget()
	if err != nil {
		return err
	}
	status, err := runtime.InspectDirectoryOwnerRecord(ctx, target.Directory, target.Owner, target.SchedulerIdentity)
	if err != nil {
		return err
	}
	if status != runtime.OwnerRecordAbsent {
		return fmt.Errorf("%w: directory %s, owner record %s; set STARPORT_CATALOG_STATE_DIR to a new empty directory for adoption and the fresh gateway",
			ErrCatalogDirectoryOccupied, target.Directory, status)
	}
	return nil
}

func populatedRecordDigest(s string) bool {
	decoded, err := hex.DecodeString(s)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == s
}

// PreparePopulatedRecovery verifies C and H against the fenced live deployment and retains the native control observation.
// It places no claim. An exact retry returns the retained record and does not observe the controls again.
func PreparePopulatedRecovery(ctx context.Context, cfg *config.Config, request PopulatedRecoveryRequest) (result PopulatedRecoveryPreparation, resultErr error) {
	if ctx == nil || cfg == nil || request.Validate() != nil || request.ExpectedPreparedSHA256 != "" || request.Activation.ExpectedDecisionSHA256 != "" {
		return result, recovery.ErrConflict
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return result, recovery.ErrConflict
	}
	if err := checkPopulatedBackends(cfg); err != nil {
		return result, err
	}
	activation := request.Activation
	source, err := inspectPopulatedSource(ctx, cfg, activation)
	if err != nil {
		return result, err
	}
	history, err := source.VerifyHistoryPackage(ctx, activationHistoryRequest(activation))
	if err != nil {
		return result, err
	}
	identity, err := source.AdoptionIdentity(history, request.PriorApproval)
	if err != nil {
		return result, err
	}
	directory, err := productfiles.ExistingDirectory(request.PreparationDirectory)
	if err != nil {
		return result, err
	}
	_, body, err := readPopulatedPreparation(ctx, directory, request)
	if err == nil {
		return PopulatedRecoveryPreparation{PreparedSHA256: canonicalRecordSHA256(body), NextAction: "activate with the retained prepared digest"}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	n, err := openPopulatedRecoveryNative(ctx, cfg, activation)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, n.close()) }()
	current, err := n.witness.Current(ctx, identity.Boundary.DeploymentID)
	if err != nil || current != source.CapturedBoundary() {
		return result, errors.Join(recovery.ErrConflict, err)
	}
	if _, err := verifyCanonicalFilesInPlace(ctx, cfg, activation.Prepare, source); err != nil {
		return result, err
	}
	if err := checkPopulatedCatalogDirectory(ctx, cfg); err != nil {
		return result, err
	}
	targets, err := n.populatedTargets()
	if err != nil {
		return result, err
	}
	controls, err := recovery.ObservePopulatedControls(ctx, targets)
	if err != nil {
		return result, err
	}
	body, err = retainPopulatedPreparation(ctx, directory, populatedPreparationRecord{Version: 1, Request: request.Original(), Controls: controls})
	if err != nil {
		return result, err
	}
	return PopulatedRecoveryPreparation{PreparedSHA256: canonicalRecordSHA256(body), NextAction: "activate with the retained prepared digest"}, nil
}

// ActivatePopulatedRecovery places the populated claims from the retained observation and accepts H with the closed-adoption transition.
// The shared activation phases then run unchanged. Exact sealed retries reopen owner evidence without a new claim.
func ActivatePopulatedRecovery(ctx context.Context, cfg *config.Config, request PopulatedRecoveryRequest) (result RecoveryActivationResult, resultErr error) {
	if ctx == nil || cfg == nil || request.Validate() != nil || request.ExpectedPreparedSHA256 == "" {
		return result, recovery.ErrConflict
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return result, recovery.ErrConflict
	}
	if err := checkPopulatedBackends(cfg); err != nil {
		return result, err
	}
	source, transition, err := openPopulatedPreparation(ctx, cfg, request)
	if err != nil {
		return result, err
	}
	n, err := openPopulatedRecoveryNative(ctx, cfg, request.Activation)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, n.close()) }()
	return activateRecovery(ctx, cfg, n, source, request.Activation, transition)
}

// InspectPopulatedRecovery reports sealed historical completion and current permission of one populated adoption.
// It never claims, prepares, repairs, releases, captures, or renews permission.
func InspectPopulatedRecovery(ctx context.Context, cfg *config.Config, request PopulatedRecoveryRequest) (result RecoveryActivationResult, resultErr error) {
	if ctx == nil || cfg == nil || request.Validate() != nil || request.ExpectedPreparedSHA256 == "" || request.Activation.ExpectedDecisionSHA256 == "" {
		return result, recovery.ErrConflict
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return result, recovery.ErrConflict
	}
	if err := checkPopulatedBackends(cfg); err != nil {
		return result, err
	}
	source, transition, err := openPopulatedPreparation(ctx, cfg, request)
	if err != nil {
		return result, err
	}
	n, err := openPopulatedRecoveryNative(ctx, cfg, request.Activation)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, n.close()) }()
	return inspectRecoveryActivation(ctx, cfg, n, source, request.Activation, transition)
}

func inspectPopulatedSource(ctx context.Context, cfg *config.Config, request RecoveryActivationRequest) (*recovery.RestoreSource, error) {
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return nil, err
	}
	source, err := recovery.InspectRestoreSource(ctx, request.Prepare.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	if err != nil {
		return nil, err
	}
	if source.DeploymentID() != cfg.EffectivePaths().DeploymentID {
		return nil, recovery.ErrConflict
	}
	return source, nil
}

// openPopulatedPreparation verifies C and the retained prepared record, and selects the closed-adoption transition.
func openPopulatedPreparation(ctx context.Context, cfg *config.Config, request PopulatedRecoveryRequest) (*recovery.RestoreSource, recoveryActivationTransition, error) {
	source, err := inspectPopulatedSource(ctx, cfg, request.Activation)
	if err != nil {
		return nil, recoveryActivationTransition{}, err
	}
	directory, err := productfiles.ExistingDirectory(request.PreparationDirectory)
	if err != nil {
		return nil, recoveryActivationTransition{}, err
	}
	record, body, err := readPopulatedPreparation(ctx, directory, request)
	if err != nil {
		return nil, recoveryActivationTransition{}, err
	}
	if canonicalRecordSHA256(body) != request.ExpectedPreparedSHA256 {
		return nil, recoveryActivationTransition{}, recovery.ErrConflict
	}
	return source, recoveryActivationTransition{adoption: &populatedAdoption{prior: record.Request.PriorApproval, controls: record.Controls}}, nil
}

// readPopulatedPreparation returns os.ErrNotExist only when no prepared record exists.
// A retained record for other inputs refuses.
func readPopulatedPreparation(ctx context.Context, directory *productfiles.Directory, request PopulatedRecoveryRequest) (populatedPreparationRecord, []byte, error) {
	var record populatedPreparationRecord
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return record, nil, err
	}
	body, err := directory.ReadFile(populatedRecoveryPreparation, populatedRecoveryMaxBytes)
	if err != nil {
		return record, nil, err
	}
	if json.Unmarshal(body, &record, json.RejectUnknownMembers(true)) != nil || record.Version != 1 {
		return record, nil, recovery.ErrConflict
	}
	canonical, err := json.Marshal(record, json.Deterministic(true))
	if err != nil || !bytes.Equal(body, canonical) || record.Request != request.Original() {
		return record, nil, recovery.ErrConflict
	}
	return record, body, nil
}

func retainPopulatedPreparation(ctx context.Context, directory *productfiles.Directory, record populatedPreparationRecord) ([]byte, error) {
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if len(body) > populatedRecoveryMaxBytes {
		return nil, recovery.ErrConflict
	}
	if err := directory.CompareAndPublish(ctx, populatedRecoveryPreparation, nil, body); err != nil {
		return nil, err
	}
	return body, nil
}
