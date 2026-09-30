package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

type sealedRecoveryActivation struct {
	request    RecoveryActivationRequest
	record     recoveryActivationRecord
	journal    *recovery.ActivationJournal
	source     *recovery.RestoreSource
	facts      recovery.ActivationFacts
	identity   recovery.PreparedImportIdentity
	compiled   *catalog.CompiledTopology
	encryption *credentials.EncryptionService
}

func openSealedActivation(ctx context.Context, cfg *config.Config, request RecoveryActivationRequest, n *recoveryActivationNative, source *recovery.RestoreSource, body []byte, digest string) (*sealedRecoveryActivation, error) {
	return openSealedActivationWithCompiled(ctx, cfg, request, n, source, body, digest, nil, nil)
}

// The initial path can retain its actual checked owner capability. Restart must reopen original evidence.
func openSealedActivationWithCompiled(ctx context.Context, cfg *config.Config, request RecoveryActivationRequest, _ *recoveryActivationNative, source *recovery.RestoreSource, body []byte, digest string, compiled *catalog.CompiledTopology, final *recovery.ClosedFinalHistory) (*sealedRecoveryActivation, error) {
	var record recoveryActivationRecord
	if len(body) > recovery.ActivationDecisionMaxBytes || json.Unmarshal(body, &record, json.RejectUnknownMembers(true)) != nil || record.Version != 1 || record.Preparation.Version != 1 || record.Preparation.Request != request.Original() {
		return nil, recovery.ErrConflict
	}
	canonical, err := json.Marshal(record, json.Deterministic(true))
	if err != nil || !bytes.Equal(body, canonical) {
		return nil, recovery.ErrConflict
	}
	journal, err := inspectActivationCompletionJournal(ctx, request.ActivationDirectory, digest)
	if err != nil {
		return nil, err
	}
	original, err := journal.Record()
	if err != nil || !bytes.Equal(original, body) {
		return nil, recovery.ErrConflict
	}
	if compiled == nil {
		compiled, err = catalog.InspectRecoveryTopology(ctx, source, record.Preparation.Transfer, record.Preparation.Compiled, canonicalRecordSHA256(record.Preparation.Compiled))
		if err != nil {
			return nil, err
		}
	} else {
		checked, recordErr := compiled.Record()
		if recordErr != nil || !bytes.Equal(checked, record.Preparation.Compiled) {
			return nil, recovery.ErrConflict
		}
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return nil, err
	}
	var actual []byte
	var facts recovery.ActivationFacts
	if final == nil {
		retained := recovery.RetainedActivationHistoryRequest{History: recovery.HistoryPackageRequest{Directory: request.History.HistoryDirectory, ManifestSHA256: request.History.HistorySHA256, TargetSHA256: request.History.ExpectedTargetSHA256, Operation: request.History.Operation}, Directory: request.History.JournalDirectory, DecisionSHA256: canonicalRecordSHA256(record.History), ScratchDirectory: request.History.ScratchDirectory, Attestation: request.History.Attestation}
		history, openErr := recovery.OpenRetainedActivationHistory(ctx, source, retained, encryption, compiled.InspectPreparedCapturedCatalog)
		if openErr != nil {
			return nil, openErr
		}
		actual, err = history.Record()
		if err == nil {
			facts, err = history.ActivationFacts()
		}
	} else {
		actual, err = final.Record()
		if err == nil {
			facts, err = final.ActivationFacts()
		}
	}
	if err != nil || !bytes.Equal(actual, record.History) {
		return nil, recovery.ErrConflict
	}

	if facts.Boundary != record.Preparation.Transfer.DestinationBoundary || facts.Positions.KV.Sequence <= 0 || facts.Positions.SQL.Sequence <= 0 {
		return nil, recovery.ErrConflict
	}
	identity, err := source.ImportIdentity(request.Prepare.Operation)
	if err != nil {
		return nil, err
	}
	importBody, err := json.Marshal(identity, json.Deterministic(true))
	if err != nil || canonicalRecordSHA256(importBody) != record.Preparation.Transfer.ClosedImportSHA256 {
		return nil, recovery.ErrConflict
	}
	return &sealedRecoveryActivation{request: request, record: record, journal: journal, source: source, facts: facts, identity: identity, compiled: compiled, encryption: encryption}, nil
}

func (s *sealedRecoveryActivation) checkCurrent(ctx context.Context, cfg *config.Config, n *recoveryActivationNative, insideSQL bool) error {
	if !insideSQL {
		if err := s.checkExternalInputs(ctx, cfg, n); err != nil {
			return err
		}
	}
	// The canonical capability also checks the exact current loaded settings, source, trust, and administrator choices.
	if _, err := inspectCanonicalFilesWithSource(ctx, cfg, s.request.Prepare, s.record.Preparation.Canonical, canonicalRecordSHA256(s.record.Preparation.Canonical), s.source); err != nil {
		return err
	}
	target, options, err := catalogSettings(cfg).RecoveryTopologyTarget()
	if err != nil {
		return err
	}
	proof, err := s.compiled.InspectMaterialization(ctx, target, s.record.Materialization, options...)
	if err != nil {
		return err
	}
	if err := s.compiled.CheckSelection(ctx, proof, passiveActivationCatalog{n.store}); err != nil {
		return err
	}
	clock, err := inspectActivationClock(ctx, cfg)
	if err != nil {
		return err
	}
	_, err = s.compiled.InspectRetainedPermission(ctx, catalogSettings(cfg), clock, s.record.Permission, canonicalRecordSHA256(s.record.Permission))
	return err
}

// SQL approval checks these owners before its transaction to avoid recursive SQL reads.
func (s *sealedRecoveryActivation) checkExternalInputs(ctx context.Context, cfg *config.Config, n *recoveryActivationNative) error {
	actual, err := configuredRecoveryTarget(ctx, cfg, n.db, n.blobs, s.request.History.ValkeyIncarnation)
	if err != nil || actual != s.request.History.ExpectedTargetSHA256 {
		return errors.Join(recovery.ErrConflict, err)
	}
	inputs, err := InspectRecoveryOperatorInputs(ctx, cfg, n.db, n.blobs, operatorActivationRequest(s.request))
	if err != nil {
		return err
	}
	body, err := inputs.Record()
	if err != nil || !bytes.Equal(body, s.record.Preparation.Operator) {
		return recovery.ErrConflict
	}
	return nil
}

func (s *sealedRecoveryActivation) nativeCompletion(ctx context.Context, n *recoveryActivationNative) ([3]bool, error) {
	var completed [3]bool
	errorsByPhase := []error{
		n.blobInspect.CheckActivatedImportAt(ctx, s.identity.ComponentOperation, s.identity.BlobOriginal, s.facts.Positions.Blobs, s.journal.Digest()),
		n.kvInspect.CheckActivatedImportAt(ctx, s.identity.KVClaim, s.facts.Positions.KV, s.journal.Digest()),
		n.db.CheckActivatedRelationalImportAt(ctx, s.identity.SQLOriginal, s.identity.SQL, s.facts.Positions.SQL, s.journal.Digest()),
	}
	for i, err := range errorsByPhase {
		completed[i] = err == nil
		if err == nil {
			continue
		}
		// A failed receipt check is not proof of absence. Its owner must prove the original closed claim.
		var closedErr error
		switch i {
		case 0:
			inspector, ok := n.blobs.(blob.ImportUnreleasedInspector)
			if !ok {
				return completed, recovery.ErrConflict
			}
			closedErr = inspector.CheckUnreleasedImport(ctx, s.identity.ComponentOperation, s.identity.BlobOriginal)
		case 1:
			inspector, ok := n.kv.(storage.ImportUnreleasedInspector)
			if !ok {
				return completed, recovery.ErrConflict
			}
			closedErr = inspector.CheckUnreleasedImport(ctx, s.identity.KVClaim)
		case 2:
			closedErr = n.db.CheckUnreleasedRelationalImport(ctx, s.identity.SQLOriginal, s.identity.SQL)
		}
		if closedErr != nil {
			return completed, errors.Join(recovery.ErrConflict, err, closedErr)
		}
	}
	for i := 1; i < len(completed); i++ {
		if completed[i] && !completed[i-1] {
			return completed, recovery.ErrConflict
		}
	}
	for i := 0; i < s.journal.CompletedPhases(); i++ {
		if !completed[i] {
			return completed, errors.Join(recovery.ErrConflict, errorsByPhase[i])
		}
	}
	return completed, nil
}

func (s *sealedRecoveryActivation) release(ctx context.Context, cfg *config.Config, n *recoveryActivationNative) (RecoveryActivationResult, error) {
	result := RecoveryActivationResult{DecisionSHA256: s.journal.Digest(), Restricted: true, NextAction: "retry activation with the original decision"}
	complete, err := s.nativeCompletion(ctx, n)
	if err != nil {
		return result, err
	}
	for index, phase := range []recovery.ActivationPhase{recovery.ActivationBlobs, recovery.ActivationKV, recovery.ActivationSQL} {
		if !complete[index] {
			var currentErr error
			if phase == recovery.ActivationSQL {
				// The SQL approval callback checks current files, materialization, and permission inside the transaction.
				currentErr = s.checkExternalInputs(ctx, cfg, n)
			} else {
				currentErr = s.checkCurrent(ctx, cfg, n, false)
			}
			if currentErr != nil {
				return result, currentErr
			}
			current, err := n.witness.Current(ctx, s.facts.Boundary.DeploymentID)
			if err != nil || current != s.facts.Boundary {
				return result, errors.Join(recovery.ErrConflict, err)
			}
			if err := n.db.CheckRelationalImportPosition(ctx, s.identity.SQLOriginal, s.identity.SQL, s.facts.Positions.SQL); err != nil {
				return result, err
			}
			switch phase {
			case recovery.ActivationBlobs:
				err = n.blobActivate.ActivateImportAt(ctx, s.identity.ComponentOperation, s.identity.BlobOriginal, s.facts.Positions.Blobs, s.journal.Digest())
			case recovery.ActivationKV:
				if err := n.blobInspect.CheckActivatedImportAt(ctx, s.identity.ComponentOperation, s.identity.BlobOriginal, s.facts.Positions.Blobs, s.journal.Digest()); err != nil {
					return result, err
				}
				err = n.kvActivate.ActivateImportAt(ctx, s.identity.KVClaim, s.facts.Positions.KV, s.journal.Digest())
			case recovery.ActivationSQL:
				err = s.approveSQL(ctx, cfg, n)
			}
			if err != nil {
				return result, err
			}
			complete, err = s.nativeCompletion(ctx, n)
			if err != nil || !complete[index] {
				return result, errors.Join(recovery.ErrConflict, err)
			}
		}
		// A lost phase publication reply records only the already checked native commit.
		if index < s.journal.CompletedPhases() && s.journal.CompletionPhase() != phase {
			result.CompletedPhases = index + 1
			continue
		}
		if err := s.journal.CompletePhase(ctx, phase, func(ctx context.Context) error {
			completion, err := s.nativeCompletion(ctx, n)
			if err != nil || !completion[index] {
				return errors.Join(recovery.ErrConflict, err)
			}
			return nil
		}); err != nil {
			return result, err
		}
		result.CompletedPhases = index + 1
	}
	result.HistoricallyComplete = true
	result.NextAction = "start a fresh gateway and verify readiness"
	result.CurrentAdmissionValid = s.currentAdmission(ctx, cfg, n) == nil
	result.Restricted = !result.CurrentAdmissionValid
	if result.Restricted {
		result.NextAction = "inspect current catalog permission and deployment approval"
	}
	return result, nil
}

func (s *sealedRecoveryActivation) approveSQL(ctx context.Context, cfg *config.Config, n *recoveryActivationNative) error {
	check := func(ctx context.Context) error {
		if err := n.blobInspect.CheckActivatedImportAt(ctx, s.identity.ComponentOperation, s.identity.BlobOriginal, s.facts.Positions.Blobs, s.journal.Digest()); err != nil {
			return err
		}
		if err := n.kvInspect.CheckActivatedImportAt(ctx, s.identity.KVClaim, s.facts.Positions.KV, s.journal.Digest()); err != nil {
			return err
		}
		return s.checkCurrent(ctx, cfg, n, true)
	}
	evidence := s.request.Prepare.Operation.FencingEvidence
	if cfg.RuntimeStorage().Type == storage.StorageTypeValkey {
		provider, ok := n.store.(storage.IncarnationProvider)
		if !ok {
			return recovery.ErrConflict
		}
		_, err := n.witness.ApproveImportedAuthorityCheckedAt(ctx, provider, recovery.ImportedAuthorityRequest{Closed: s.facts.Boundary, BackendID: s.request.History.ValkeyIncarnation, Evidence: evidence, OperationID: s.identity.SQL.OperationID, Snapshot: s.identity.SQLOriginal, Import: s.identity.SQL, DecisionSHA256: s.journal.Digest()}, s.facts.Positions.SQL, check)
		return err
	}
	targetDigest, err := cfg.RuntimeStorage().RecoveryTargetSHA256("")
	if err != nil {
		return err
	}
	target, err := storage.OpenLocalRecoveryTarget(ctx, n.store, targetDigest)
	if err != nil {
		return err
	}
	_, err = n.witness.CompleteImportedLocalCheckedAt(ctx, target, recovery.ImportedLocalCompletionRequest{Closed: s.facts.Boundary, Snapshot: s.identity.SQLOriginal, Import: s.identity.SQL, OperationID: s.identity.SQL.OperationID, DecisionSHA256: s.journal.Digest(), Evidence: evidence, LocalTargetSHA256: targetDigest, KVClaim: s.identity.KVClaim, KVPosition: s.facts.Positions.KV}, s.facts.Positions.SQL, check)
	return err
}
func (s *sealedRecoveryActivation) currentAdmission(ctx context.Context, cfg *config.Config, n *recoveryActivationNative) error {
	if err := s.checkCurrent(ctx, cfg, n, false); err != nil {
		return err
	}
	if cfg.RuntimeStorage().Type == storage.StorageTypeValkey {
		provider, ok := n.store.(storage.IncarnationProvider)
		if !ok {
			return recovery.ErrConflict
		}
		_, err := n.witness.OpenAuthority(ctx, provider, s.facts.Boundary.DeploymentID)
		return err
	}
	targetDigest, err := cfg.RuntimeStorage().RecoveryTargetSHA256("")
	if err != nil {
		return err
	}
	target, err := storage.OpenLocalRecoveryTarget(ctx, n.store, targetDigest)
	if err != nil {
		return err
	}
	return n.witness.CheckImportedLocalCompletionAt(ctx, target, recovery.ImportedLocalCompletionRequest{Closed: s.facts.Boundary, Snapshot: s.identity.SQLOriginal, Import: s.identity.SQL, OperationID: s.identity.SQL.OperationID, DecisionSHA256: s.journal.Digest(), Evidence: s.request.Prepare.Operation.FencingEvidence, LocalTargetSHA256: targetDigest, KVClaim: s.identity.KVClaim, KVPosition: s.facts.Positions.KV}, s.facts.Positions.SQL)
}

func inspectActivationCompletionJournal(ctx context.Context, path, digest string) (*recovery.ActivationJournal, error) {
	journal, originalErr := recovery.InspectActivationJournal(ctx, path, digest)
	if originalErr == nil {
		return journal, nil
	}
	for _, phase := range []recovery.ActivationPhase{recovery.ActivationBlobs, recovery.ActivationKV, recovery.ActivationSQL} {
		journal, err := recovery.InspectActivationPhaseCompletion(ctx, path, digest, phase)
		if err == nil {
			return journal, nil
		}
	}
	return nil, originalErr
}
