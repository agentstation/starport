package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// RecoveryActivationResult reports the recovery owner's procedure outcome.
type RecoveryActivationResult = recovery.ActivationResult

// ActivateRecovery coordinates prepared native imports without starting a gateway.
// It binds original backup/history and current operator choices, then releases blobs, KV, and SQL.
// Exact sealed retries reopen owner evidence and native receipts without preparation or permission renewal.
func ActivateRecovery(ctx context.Context, cfg *config.Config, request RecoveryActivationRequest) (result RecoveryActivationResult, resultErr error) {
	if ctx == nil || cfg == nil || request.Validate() != nil {
		return result, recovery.ErrConflict
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return result, recovery.ErrConflict
	}
	// One activation decodes the same catalog payload many times.
	// The scope retains complete decodes for this operation only and supplies no authority.
	release := catalogs.RetainDecodedCatalogs()
	defer release()
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return result, err
	}
	source, err := recovery.InspectRestoreSource(ctx, request.Prepare.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	if err != nil {
		return result, err
	}
	if source.DeploymentID() != cfg.EffectivePaths().DeploymentID {
		return result, recovery.ErrConflict
	}
	n, err := openRecoveryActivationNative(ctx, cfg, request)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, n.close()) }()
	return activateRecovery(ctx, cfg, n, source, request, importActivation)
}

// activateRecovery runs the shared phases after the transition places its native claims.
// An exact sealed retry reopens owner evidence and skips the claim.
func activateRecovery(ctx context.Context, cfg *config.Config, n *recoveryActivationNative, source *recovery.RestoreSource, request RecoveryActivationRequest, transition recoveryActivationTransition) (result RecoveryActivationResult, resultErr error) {
	directory, err := productfiles.ExistingDirectory(request.ActivationDirectory)
	if err != nil {
		return result, err
	}
	body, err := directory.ReadFile("decision.json", recovery.ActivationDecisionMaxBytes)
	if err == nil {
		digest := canonicalRecordSHA256(body)
		if request.ExpectedDecisionSHA256 != digest {
			return result, recovery.ErrConflict
		}
		checked, err := openSealedActivation(ctx, cfg, request, n, source, body, digest, transition)
		if err != nil {
			return result, err
		}
		return checked.release(ctx, cfg, n)
	}
	if !errors.Is(err, os.ErrNotExist) || request.ExpectedDecisionSHA256 != "" {
		return result, errors.Join(recovery.ErrConflict, err)
	}
	if err := transition.claim(ctx, cfg, n, source, request); err != nil {
		return result, err
	}
	if err := checkUnsealedActivation(ctx, cfg, n, source, request, transition); err != nil {
		return result, err
	}
	checked, err := prepareRecoveryActivationDecision(ctx, cfg, n, source, request, directory, transition)
	if err != nil {
		return result, err
	}
	return checked.release(ctx, cfg, n)
}

func checkUnsealedActivation(ctx context.Context, cfg *config.Config, n *recoveryActivationNative, source *recovery.RestoreSource, request RecoveryActivationRequest, transition recoveryActivationTransition) error {
	identity, err := transition.identity(ctx, source, request)
	if err != nil {
		return err
	}
	kv, ok := n.kv.(storage.ImportUnreleasedInspector)
	if !ok {
		return recovery.ErrConflict
	}
	if err := kv.CheckUnreleasedImport(ctx, identity.KVClaim); err != nil {
		return err
	}
	if err := n.db.CheckUnreleasedRelationalImport(ctx, identity.SQLOriginal, identity.SQL); err != nil {
		return err
	}
	boundary, err := n.witness.Current(ctx, cfg.EffectivePaths().DeploymentID)
	if err != nil || boundary.Open {
		return errors.Join(recovery.ErrConflict, err)
	}
	inspector, ok := n.blobs.(blob.ImportUnreleasedInspector)
	if !ok {
		return recovery.ErrConflict
	}
	return inspector.CheckUnreleasedImport(ctx, identity.ComponentOperation, identity.BlobOriginal)
}

func prepareRecoveryActivation(ctx context.Context, cfg *config.Config, n *recoveryActivationNative, source *recovery.RestoreSource, request RecoveryActivationRequest, directory *productfiles.Directory, transition recoveryActivationTransition) (recoveryActivationPreparationRecord, *catalog.CompiledTopology, error) {
	retained, _, err := readActivationPreparation(ctx, directory)
	if err == nil {
		if retained.Request != request.Original() {
			return retained, nil, recovery.ErrConflict
		}
		return retained, nil, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return retained, nil, err
	}
	if !request.PreserveTargetWorkspace {
		return retained, nil, recovery.ErrConflict
	}
	if err := catalogSettings(cfg).PrepareRecoveryTopologyDirectory(ctx); err != nil {
		return retained, nil, err
	}
	canonical, err := transition.canonicalFiles(ctx, cfg, n, source, request)
	if err != nil {
		return retained, nil, err
	}
	canonicalBody, err := canonical.Record()
	if err != nil {
		return retained, nil, err
	}
	inputsRequest := operatorActivationRequest(request)
	inputs, err := VerifyRecoveryOperatorInputs(ctx, cfg, n.db, n.blobs, inputsRequest)
	if err != nil {
		return retained, nil, err
	}
	inputsBody, err := inputs.Record()
	if err != nil {
		return retained, nil, err
	}
	history, err := source.VerifyHistoryPackage(ctx, activationHistoryRequest(request))
	if err != nil {
		return retained, nil, err
	}
	accepted, err := transition.accept(ctx, n, source, history, request)
	if err != nil {
		return retained, nil, err
	}
	boundary, err := accepted.Boundary()
	if err != nil {
		return retained, nil, err
	}
	identity, err := transition.identity(ctx, source, request)
	if err != nil {
		return retained, nil, err
	}
	encoded, err := json.Marshal(identity, json.Deterministic(true))
	if err != nil {
		return retained, nil, err
	}
	destinationFleet := cfg.RuntimeStorage().Type == storage.StorageTypeValkey
	direction, err := catalog.DeriveRecoveryTopologyDirection(ctx, source, destinationFleet)
	if err != nil {
		return retained, nil, err
	}
	destination := runtime.FleetIdentity{}
	if destinationFleet {
		if boundary.Epoch <= 0 {
			return retained, nil, recovery.ErrConflict
		}
		destination = runtime.FleetIdentity{DeploymentID: boundary.DeploymentID, RecoveryEpoch: uint64(boundary.Epoch), BackendID: request.History.ValkeyIncarnation}
	}
	transfer := catalog.TopologyTransferRequest{Direction: direction, DestinationIdentity: destination, DestinationBoundary: boundary, Operation: request.Prepare.Operation, AcceptedDecisionSHA256: accepted.Digest(), ClosedImportSHA256: canonicalRecordSHA256(encoded)}
	compiled, err := catalog.CompileRecoveryTopology(ctx, source, transfer)
	if err != nil {
		return retained, nil, err
	}
	compiledBody, err := compiled.Record()
	if err != nil {
		return retained, nil, err
	}
	retained = recoveryActivationPreparationRecord{Version: 1, Request: request.Original(), Transfer: transfer, Operator: inputsBody, Canonical: canonicalBody, Compiled: compiledBody}
	if err := retainActivationPreparation(ctx, directory, retained); err != nil {
		return retained, nil, err
	}
	return retained, compiled, nil
}

func operatorActivationRequest(request RecoveryActivationRequest) RecoveryOperatorInputsRequest {
	return RecoveryOperatorInputsRequest{Operation: request.Prepare.Operation, JournalDirectory: request.ActivationDirectory, ExpectedTargetSHA256: request.History.ExpectedTargetSHA256, ValkeyIncarnation: request.History.ValkeyIncarnation}
}
