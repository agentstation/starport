package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/credentials"
)

func openRetainedActivationRunner(ctx context.Context, source *RestoreSource, request RetainedActivationHistoryRequest, encryption *credentials.EncryptionService) (*historyRunner, historyJournalState, error) {
	history, err := source.VerifyHistoryPackage(ctx, request.History)
	if err != nil {
		return nil, historyJournalState{}, err
	}
	if history.state.manifest.Disposition != historyReplayComplete || !request.Attestation.CompleteInterval {
		return nil, historyJournalState{}, ErrConflict
	}
	if err := checkHistoryRotationOrder(history.state.manifest.Steps); err != nil {
		return nil, historyJournalState{}, err
	}
	accepted, err := prepareHistoryAcceptance(source, history, HistoryAcceptanceRequest{Directory: request.Directory, Attestation: request.Attestation})
	if err != nil {
		return nil, historyJournalState{}, err
	}
	directory, err := productfiles.ExistingDirectory(request.Directory)
	if err != nil {
		return nil, historyJournalState{}, err
	}
	body, err := directory.ReadFile("acceptance.json", historyManifestMaxBytes)
	if err != nil || !bytes.Equal(body, accepted.body) {
		return nil, historyJournalState{}, errors.Join(ErrConflict, err)
	}
	identity, err := source.ImportIdentity(request.History.Operation)
	if err != nil {
		return nil, historyJournalState{}, err
	}
	runner := &historyRunner{accepted: &AcceptedHistory{state: accepted}, identity: identity, directory: directory,
		request: HistoryReplayRequest{TargetSHA256: request.History.TargetSHA256, ScratchDirectory: request.ScratchDirectory},
		targets: HistoryReplayTargets{Encryption: encryption}}
	runBytes, err := runner.readJournalRecord(ctx, "runner.json", &runner.run)
	if err != nil || runBytes == nil || runner.run.Version != 1 || runner.run.AcceptanceSHA256 != runner.accepted.Digest() ||
		runner.run.HistorySHA256 != history.Digest() || runner.run.TargetSHA256 != request.History.TargetSHA256 ||
		runner.run.Authority != historyAcceptedAuthority(runner.accepted) || runner.run.ValidatedAt.IsZero() {
		return nil, historyJournalState{}, errors.Join(ErrConflict, err)
	}
	runner.runBytes = bytes.Clone(runBytes)
	if runner.run.CatalogPreparation != nil {
		original := *runner.run.CatalogPreparation
		runner.request.CatalogPreparation = &original
	}
	journal, err := runner.scanJournal(ctx)
	if err != nil {
		return nil, journal, err
	}
	if err := retainedCatalogJournal(ctx, runner, &journal); err != nil {
		return nil, journal, err
	}
	if journal.pending != nil || journal.count != len(history.state.manifest.Steps) || !journal.kvRotated || !journal.sqlRotated {
		return nil, journal, ErrConflict
	}
	return runner, journal, nil
}

// retainedCatalogJournal requires the original declaration and the complete passively checked catalog chain.
// It grants no permission, preparation, repair, or native target access.
func retainedCatalogJournal(ctx context.Context, runner *historyRunner, state *historyJournalState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runner.run.CatalogPreparation == nil {
		if state.catalog != nil {
			return ErrConflict
		}
		return nil
	}
	if state.catalog == nil || state.catalog.pending != nil || state.catalog.count != runner.run.CatalogPreparation.StageCount+1 || state.catalog.complete == "" {
		return ErrConflict
	}
	return nil
}

func readRetainedActivationFinal(ctx context.Context, runner *historyRunner, journal historyJournalState, decision string, inspectors []CapturedKVInspector) (closedFinalRecord, []byte, error) {
	var record closedFinalRecord
	body, err := readRetainedFinalRecord(ctx, runner.directory, closedFinalHistoryFile, &record)
	if err != nil || body == nil || historySHA256(body) != decision {
		return record, nil, errors.Join(ErrConflict, err)
	}
	var preparation closedFinalRecord
	preparedBytes, err := readRetainedFinalRecord(ctx, runner.directory, closedFinalPreparationFile, &preparation)
	if err != nil || preparedBytes == nil {
		return record, nil, errors.Join(ErrConflict, err)
	}
	if err := checkRetainedFinalPreparation(runner, journal, preparation, preparedBytes); err != nil {
		return record, nil, err
	}
	if err := validateClosedFinalRecord(record, preparation); err != nil {
		return record, nil, err
	}
	namespace, err := runner.directory.ExistingChild(preparation.GraphDirectory)
	if err != nil {
		return record, nil, err
	}
	if err := namespace.CheckNoPendingPublications(ctx); err != nil {
		return record, nil, err
	}
	attempt, err := namespace.ReadFile(filepath.Base(record.GraphDirectory)+".json", historyManifestMaxBytes)
	if err != nil || !bytes.Equal(attempt, body) {
		return record, nil, errors.Join(ErrConflict, err)
	}
	graph := filepath.Join(runner.accepted.state.directory, record.GraphDirectory)
	if err := checkRetainedSnapshotReceipt(ctx, filepath.Join(graph, historyOwnerKV), record.Graph.KV); err != nil {
		return record, nil, err
	}
	if err := checkRetainedSnapshotReceipt(ctx, filepath.Join(graph, historyOwnerSQL), record.Graph.SQL); err != nil {
		return record, nil, err
	}
	if _, err := productfiles.ExistingDirectory(graph); err != nil {
		return record, nil, err
	}
	references, err := inspectImportedCopies(ctx, filepath.Join(graph, "kv"), filepath.Join(graph, "sql"), filepath.Join(graph, "blobs.tar"),
		runner.request.ScratchDirectory, record.GraphRequest, record.Graph, runner.targets.Encryption, closedFinalInspectors(record.Authority, inspectors)...)
	if err != nil {
		return record, nil, err
	}
	actual, err := json.Marshal(references, json.Deterministic(true))
	if err != nil {
		return record, nil, err
	}
	expected, err := json.Marshal(record.Graph.References, json.Deterministic(true))
	if err != nil || !bytes.Equal(actual, expected) {
		return record, nil, ErrConflict
	}
	return record, body, ctx.Err()
}

func checkRetainedFinalPreparation(runner *historyRunner, journal historyJournalState, preparation closedFinalRecord, body []byte) error {
	manifest := runner.accepted.state.history.manifest
	wanted := closedFinalRecord{Version: 1, BackupSHA256: manifest.BackupSHA256, PreparedSHA256: manifest.PreparedSHA256,
		AcceptanceSHA256: runner.accepted.Digest(), HistorySHA256: runner.accepted.state.history.digest, RunSHA256: historySHA256(runner.runBytes),
		Replay: runner.report(journal), Attestation: acceptedAttestation(runner), Authority: historyAcceptedAuthority(runner.accepted),
		GraphDirectory: preparation.GraphDirectory, GraphRequest: ImportedReferenceRequest{Boundary: runner.accepted.state.boundary,
			CapturedAt: preparation.GraphRequest.CapturedAt, KVClaim: bytes.Clone(runner.identity.KVClaim), KVPosition: journal.positions.KV,
			SQLOriginal: runner.identity.SQLOriginal, SQLIdentity: runner.identity.SQL, SQLPosition: journal.positions.SQL,
			BlobOperation: runner.identity.ComponentOperation, BlobPosition: journal.positions.Blobs, BlobOriginal: runner.identity.BlobOriginal}}
	if !validFinalGraphName(preparation.GraphDirectory) || preparation.GraphRequest.CapturedAt.IsZero() || preparation.GraphRequest.CapturedAt.Before(runner.run.ValidatedAt) {
		return ErrConflict
	}
	canonical, err := json.Marshal(wanted, json.Deterministic(true))
	if err != nil || !bytes.Equal(body, canonical) {
		return ErrConflict
	}
	return nil
}

func readRetainedFinalRecord(ctx context.Context, directory *productfiles.Directory, name string, record *closedFinalRecord) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := directory.ReadFile(name, historyManifestMaxBytes)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || json.Unmarshal(body, record, json.RejectUnknownMembers(true)) != nil {
		return nil, ErrConflict
	}
	canonical, err := json.Marshal(record, json.Deterministic(true))
	if err != nil || !bytes.Equal(body, canonical) {
		return nil, ErrConflict
	}
	return body, nil
}
