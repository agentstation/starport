package app

import (
	"bytes"
	"context"
	"encoding/json/v2"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
)

// prepareRecoveryActivationDecision finishes closed history and seals all actual owner records.
// The returned private capability grants no admission without native ordered release and current permission.
func prepareRecoveryActivationDecision(ctx context.Context, cfg *config.Config, n *recoveryActivationNative, source *recovery.RestoreSource, request RecoveryActivationRequest, directory *productfiles.Directory, transition recoveryActivationTransition) (*sealedRecoveryActivation, error) {
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return nil, err
	}
	preparation, compiled, err := prepareRecoveryActivation(ctx, cfg, n, source, request, directory, transition)
	if err != nil {
		return nil, err
	}
	if compiled == nil {
		compiled, err = catalog.InspectRecoveryTopology(ctx, source, preparation.Transfer, preparation.Compiled, canonicalRecordSHA256(preparation.Compiled))
		if err != nil {
			return nil, err
		}
	}
	history, err := source.VerifyHistoryPackage(ctx, activationHistoryRequest(request))
	if err != nil {
		return nil, err
	}
	accepted, err := transition.accept(ctx, n, source, history, request)
	if err != nil {
		return nil, err
	}
	if accepted.Digest() != preparation.Transfer.AcceptedDecisionSHA256 {
		return nil, recovery.ErrConflict
	}
	replayRequest := recovery.HistoryReplayRequest{TargetSHA256: request.History.ExpectedTargetSHA256, ScratchDirectory: request.History.ScratchDirectory, CatalogPreparation: &recovery.CatalogPreparationPlan{TopologySHA256: compiled.TopologyDigest(), StageCount: compiled.StageCount()}}
	targets := recovery.HistoryReplayTargets{KV: n.kv, Blobs: n.blobReplay, Encryption: encryption}
	prefix, err := n.witness.ReplayImportedHistoryPrefix(ctx, source, accepted, targets, replayRequest)
	if err != nil {
		return nil, err
	}
	lane, err := prefix.OpenCatalogPreparation(ctx)
	if err != nil {
		return nil, err
	}
	for index := 0; index < compiled.StageCount(); index++ {
		if err := compiled.ApplyStage(ctx, index, lane); err != nil {
			return nil, err
		}
	}
	settings := catalogSettings(cfg)
	target, options, err := settings.RecoveryTopologyTarget()
	if err != nil {
		return nil, err
	}
	materialized, err := compiled.RetainAndMaterialize(ctx, target, options...)
	if err != nil {
		return nil, err
	}
	if err := compiled.ApplySelection(ctx, materialized, lane); err != nil {
		return nil, err
	}
	if err := compiled.CheckSelection(ctx, materialized, lane); err != nil {
		return nil, err
	}
	completed, err := n.witness.ReplayImportedHistory(ctx, source, accepted, targets, replayRequest)
	if err != nil {
		return nil, err
	}
	finalRequest := recovery.ClosedFinalRequest{TargetSHA256: request.History.ExpectedTargetSHA256, Attestation: request.History.Attestation}
	final, err := n.witness.FinalizeImportedHistory(ctx, completed, finalRequest, compiled.InspectPreparedCapturedCatalog)
	if err != nil {
		return nil, err
	}
	facts, err := final.ActivationFacts()
	if err != nil {
		return nil, err
	}
	if facts.Boundary != preparation.Transfer.DestinationBoundary {
		return nil, recovery.ErrConflict
	}
	retainedHistory, err := final.Record()
	if err != nil {
		return nil, err
	}
	materialization, err := materialized.Record()
	if err != nil {
		return nil, err
	}
	clock, err := inspectActivationClock(ctx, cfg)
	if err != nil {
		return nil, err
	}
	permission, err := compiled.InspectPermission(ctx, settings, clock)
	if err != nil {
		return nil, err
	}
	permissionRecord, err := permission.Record()
	if err != nil {
		return nil, err
	}
	record := recoveryActivationRecord{Version: 1, Preparation: preparation, History: retainedHistory, Materialization: materialization, Permission: permissionRecord}
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if err := checkPreparedActivationOwners(ctx, cfg, n, source, request, transition, preparation, compiled, target, options, materialization, permission); err != nil {
		return nil, err
	}

	journal, err := recovery.SealActivationJournal(ctx, request.ActivationDirectory, body)
	if err != nil {
		return nil, err
	}
	checked, err := openSealedActivationWithCompiled(ctx, cfg, request, n, source, body, journal.Digest(), transition, compiled, final)
	if err != nil {
		return nil, err
	}
	return checked, nil
}

func checkPreparedActivationOwners(ctx context.Context, cfg *config.Config, n *recoveryActivationNative, source *recovery.RestoreSource, request RecoveryActivationRequest, transition recoveryActivationTransition, preparation recoveryActivationPreparationRecord, compiled *catalog.CompiledTopology, target catalog.TopologyRuntimeTarget, options []runtime.Option, materialization []byte, permission *catalog.TopologyPermission) error {
	// Recheck every owner before retaining the immutable application decision.
	inputs, err := InspectRecoveryOperatorInputs(ctx, cfg, n.db, n.blobs, operatorActivationRequest(request))
	if err != nil {
		return err
	}
	inputsBody, err := inputs.Record()
	if err != nil || !bytes.Equal(inputsBody, preparation.Operator) {
		return recovery.ErrConflict
	}
	if _, err := inspectCanonicalFilesWithSource(ctx, cfg, request.Prepare, preparation.Canonical, canonicalRecordSHA256(preparation.Canonical), source); err != nil {
		return err
	}
	checkedMaterialization, err := compiled.InspectMaterialization(ctx, target, materialization, options...)
	if err != nil {
		return err
	}
	if err := compiled.CheckSelection(ctx, checkedMaterialization, passiveActivationCatalog{n.store}); err != nil {
		return err
	}
	clock, err := inspectActivationClock(ctx, cfg)
	if err != nil {
		return err
	}
	if err := compiled.CheckPermission(ctx, catalogSettings(cfg), clock, permission); err != nil {
		return err
	}
	if err := checkUnsealedActivation(ctx, cfg, n, source, request, transition); err != nil {
		return err
	}
	return nil
}
