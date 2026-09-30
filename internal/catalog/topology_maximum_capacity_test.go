//go:build catalogcapacity

package catalog

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const maximumCatalogTest = "TestRecoveryCatalogMaximumSerializedInventoryRoundTrip"

type capacityCapsuleIdentity struct {
	Manifest, Recovery, Payload string
}

type capacityNativeStage struct {
	Index     int   `json:"index"`
	Mutations int   `json:"mutations"`
	Bytes     int64 `json:"bytes"`
}

type capacityMaximumEvidence struct {
	Entries               int                   `json:"entries"`
	SerializedBytes       int64                 `json:"serialized_fleet_snapshot_bytes"`
	Accepted              string                `json:"accepted_generation"`
	Candidate             string                `json:"candidate_generation"`
	OriginalManifest      string                `json:"original_manifest_sha256"`
	FleetManifest         string                `json:"fleet_manifest_sha256"`
	ReverseManifest       string                `json:"reverse_manifest_sha256"`
	ProducerBatches       []capacityBatchUsage  `json:"producer_batches"`
	ForwardStages         []capacityNativeStage `json:"forward_stages"`
	ReverseStages         []capacityNativeStage `json:"reverse_stages"`
	ForwardSealBytes      int                   `json:"forward_compiler_seal_bytes"`
	ReverseSealBytes      int                   `json:"reverse_compiler_seal_bytes"`
	RollbackEntries       int                   `json:"rollback_entries"`
	LostReplyExactRetries int                   `json:"lost_reply_exact_retries"`
}

type capacityBatchUsage struct {
	Inputs  int   `json:"inputs"`
	Raw     int64 `json:"raw_bytes"`
	Decoded int64 `json:"decoded_recovery_bytes"`
}

// capacityGuardedTarget uses native replay receipts while SQL holds the closed import boundary.
// Its maps index observed native receipts. They do not replace a native mutation or cursor check.
type capacityGuardedTarget struct {
	native  *topologyImportTestTarget
	witness *recovery.Witness
	guard   recovery.ClosedImportGuardRequest
	backup  *capacityBackup
	stages  []capacityNativeStage
}

func (t *capacityGuardedTarget) ReadCatalogTopology(ctx context.Context, key string, limit int) ([]byte, error) {
	return t.native.ReadCatalogTopology(ctx, key, limit)
}

func (t *capacityGuardedTarget) CompletedCatalogTopology(ctx context.Context, digest string, index int) (bool, error) {
	return t.native.CompletedCatalogTopology(ctx, digest, index)
}

func (t *capacityGuardedTarget) ApplyCatalogTopology(ctx context.Context, digest string, index int, mutations []storage.CompareAndSwapMutation) error {
	var size int64
	for _, mutation := range mutations {
		size += int64(len(mutation.Key)) + int64(len(mutation.ExpectedValue)) + int64(len(mutation.NewValue))
	}
	if len(mutations) == 0 || len(mutations) > 128 || size > 4<<20 {
		return storage.ErrValueTooLarge
	}
	id := fmt.Sprint(digest, "/", index)
	_, already := t.native.completed[id]
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := t.witness.GuardClosedImport(bounded, t.guard, func(ctx context.Context, _ *sql.Conn) error {
		return t.native.ApplyCatalogTopology(ctx, digest, index, mutations)
	})
	if _, completed := t.native.completed[id]; completed && !already {
		t.stages = append(t.stages, capacityNativeStage{Index: index, Mutations: len(mutations), Bytes: size})
	}
	return err
}

func capacityCloneRuntimeFiles(t *testing.T, source, destination string) {
	t.Helper()
	_, err := productfiles.NewDirectory(filepath.Dir(destination))
	require.NoError(t, err)
	require.NoError(t, filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) (resultErr error) {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			_, err = productfiles.CreateDirectory(target)
			return err
		}
		if !entry.Type().IsRegular() {
			return errors.New("capacity runtime clone requires original regular files")
		}
		input, err := os.Open(name)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, output.Close()) }()
		_, err = io.Copy(output, input)
		return err
	}))
}

func capacityImportTarget(t *testing.T, source *capacityBackup, operation string) *capacityGuardedTarget {
	t.Helper()
	root := filepath.Join(t.TempDir(), operation)
	_, err := productfiles.CreateDirectory(root)
	require.NoError(t, err)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(root)).WithEnvironment(map[string]string{"STARPORT_DEPLOYMENT_ID": source.manifest.Request.Boundary.DeploymentID, "STARPORT_INSTANCE_ID": "source-instance"}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	paths := cfg.EffectivePaths()
	settings := source.settings
	settings.StateDirectory = cfg.Catalog.StateDirectory
	capacityCloneRuntimeFiles(t, source.settings.StateDirectory, settings.StateDirectory)
	kv, err := storage.OpenBadger(storage.BadgerConfig{Path: paths.BadgerDir, SyncWrites: true, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, kv.Close()) })
	transfer := capacityRecordTransfer(t, kv)
	claim, err := json.Marshal(struct {
		Version     int
		OperationID string
		Snapshot    recovery.KVSnapshot
	}{1, operation, source.manifest.KV}, json.Deterministic(true))
	require.NoError(t, err)
	native := &topologyImportTestTarget{kv: kv, transfer: transfer, claim: claim, completed: map[string]storage.ImportReplayPosition{}}
	require.NoError(t, transfer.Claim(t.Context(), claim))
	view, err := source.source.OpenCapturedKV(t.Context())
	require.NoError(t, err)
	require.NoError(t, view.Enumerate(t.Context(), func(record storage.TransferRecord) error { return transfer.Import(t.Context(), claim, record) }))
	require.NoError(t, view.Close())
	_, err = productfiles.CreateDirectory(filepath.Dir(paths.SQLiteFile))
	require.NoError(t, err)
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: paths.SQLiteFile}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	identity := sqlstore.RelationalImportIdentity{OperationID: operation, RestrictionID: "catalog-maximum-closed"}
	require.NoError(t, db.ImportRelationalOnce(t.Context(), filepath.Join(source.directory, "sql", "starport.db"), source.manifest.SQL, source.directory, identity, func(context.Context, *sql.Conn) error { return nil }))
	directory, err := productfiles.ExistingDirectory(paths.BadgerDir)
	require.NoError(t, err)
	nativeIdentity, err := directory.Identity()
	require.NoError(t, err)
	boundary := source.manifest.Request.Boundary
	boundary.Epoch++
	boundary.BackendID = payloadDigest([]byte(nativeIdentity))
	boundary.Evidence = payloadDigest(claim)
	transition, err := json.Marshal(boundary, json.Deterministic(true))
	require.NoError(t, err)
	step := sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: boundary.Evidence, TransitionSHA256: payloadDigest(transition)}
	receipt, err := db.ReplayRelationalImport(t.Context(), source.manifest.SQL, identity, step, func(ctx context.Context, conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, "UPDATE catalog_recovery SET epoch=?,backend_id=?,evidence=?,gate_open=0,bootstrap_allowed=0 WHERE deployment_id=? AND epoch=? AND gate_open=0", boundary.Epoch, boundary.BackendID, boundary.Evidence, boundary.DeploymentID, source.manifest.Request.Boundary.Epoch)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return errors.Join(recovery.ErrConflict, err)
		}
		return nil
	})
	require.NoError(t, err)
	witness, err := recovery.New(db)
	require.NoError(t, err)
	blobs, err := blob.NewFilesystem(paths.FilesDir)
	require.NoError(t, err)
	return &capacityGuardedTarget{native: native, witness: witness,
		guard:  recovery.ClosedImportGuardRequest{Snapshot: source.manifest.SQL, Import: identity, Position: sqlstore.RelationalReplayPosition{Sequence: 1, ReceiptSHA256: receipt}, Boundary: boundary},
		backup: &capacityBackup{settings: settings, config: cfg, kv: kv, db: db, blobs: blobs, encryption: source.encryption}}
}

func capacityTransferRequest(target *capacityGuardedTarget, direction TopologyDirection, operation string) TopologyTransferRequest {
	request := TopologyTransferRequest{Direction: direction, Operation: recovery.RestoreOperation{ID: operation, FencingEvidence: "only-fixture-handles-own-native-writers"}, DestinationBoundary: target.guard.Boundary,
		AcceptedDecisionSHA256: payloadDigest([]byte(operation + target.guard.Boundary.BackendID)), ClosedImportSHA256: payloadDigest(target.native.claim)}
	if direction == TopologyLocalToFleet {
		request.DestinationIdentity = runtime.FleetIdentity{DeploymentID: target.guard.Boundary.DeploymentID, RecoveryEpoch: uint64(target.guard.Boundary.Epoch), BackendID: target.guard.Boundary.BackendID}
	}
	return request
}

func capacitySelectedCapsule(t *testing.T, settings Settings, generation catalogs.Generation) TopologyCapsule {
	t.Helper()
	checksums, err := runtime.CatalogRecoveryChecksums(t.Context(), settings.StateDirectory, settings.directoryOwner(), "capacity-source", generation)
	require.NoError(t, err)
	require.Len(t, checksums, 1)
	inputs, err := runtime.ReadCatalogRecovery(t.Context(), settings.StateDirectory, settings.directoryOwner(), "capacity-source", generation, checksums[0])
	require.NoError(t, err)
	return TopologyCapsule{Generation: generation, Recovery: inputs, SourceOrigin: string(runtime.CatalogRetentionNoDescriptor)}
}

func capacityPublishCandidate(t *testing.T, connected *Runtime) catalogs.Generation {
	t.Helper()
	observation, err := sources.NewObservation(sources.LocalCatalogID, testEmptyCatalog(t, "capacity-candidate"), sources.ObservationMetadata{ObservedAt: time.Now().UTC(), Revision: sources.Revision{Kind: sources.RevisionKindContentDigest}, Completeness: sources.ObservationCompletenessComplete, Status: sources.ObservationStatusSucceeded, Records: sources.ObservationRecordCounts{Accepted: 1}})
	require.NoError(t, err)
	_, err = connected.runtime.UpdateAcquisition(t.Context(), func(context.Context, runtime.ObservationInputs) (runtime.ObservationUpdate, error) {
		return runtime.ObservationUpdate{Observations: []sources.Observation{observation}}, nil
	}, sources.LocalCatalogID)
	require.NoError(t, err)
	generation, err := connected.candidates.Current(t.Context())
	require.NoError(t, err)
	return generation
}

func capacityLargeHistoricalCapsule(t *testing.T, id string, padding int) TopologyCapsule {
	t.Helper()
	canonical := catalogs.NewEmpty()
	require.NoError(t, canonical.SetProvider(catalogs.Provider{ID: "capacity-history", Name: strings.Repeat("x", padding)}))
	catalog, err := canonical.Build()
	require.NoError(t, err)
	generation := runtimeTestGeneration(t, id, catalog, time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC))
	// Reuse the actual recovery codec fixture with a canonical payload, rather than a padded envelope.
	capsule := topologySmallCapsule(t, id)
	capsule.Generation = generation
	body, err := json.Marshal(struct {
		Version       int                 `json:"version"`
		Baseline      catalogs.Generation `json:"baseline"`
		PublisherID   string              `json:"publisher_id"`
		Compatibility string              `json:"compatibility"`
	}{2, generation, "capacity-history", strings.Repeat("c", 64)}, json.Deterministic(true))
	require.NoError(t, err)
	capsule.Recovery = capacityRecoveryRecord(t, generation, body)
	return capsule
}

func capacitySnapshotSize(t *testing.T, capsule TopologyCapsule, request TopologyTransferRequest) int64 {
	t.Helper()
	ref, err := capsule.reference()
	require.NoError(t, err)
	// This invokes the production compiler's snapshot writer only to calibrate one payload.
	// The maximum source census still comes solely from its canonical original backup.
	compiler := CompiledTopology{request: request, inventory: topologyInventoryData{Capsules: []TopologyCapsule{capsule}, Selection: TopologySelection{Accepted: ref, Candidate: ref}}}
	immutable, controls := map[string][]byte{}, map[string][]byte{}
	require.NoError(t, compiler.compileFleet(immutable, controls))
	prefix := "catalog:fleet:{" + payloadDigest([]byte(request.DestinationIdentity.DeploymentID)) + "}:v1:"
	var inventory fleetInventory
	require.NoError(t, json.Unmarshal(controls[prefix+"inventory"], &inventory))
	require.Len(t, inventory.Entries, 1)
	return int64(inventory.Entries[0].Record.Size)
}

func capacityOriginalIdentities(t *testing.T, capsules []TopologyCapsule) map[string]capacityCapsuleIdentity {
	t.Helper()
	result := map[string]capacityCapsuleIdentity{}
	for _, capsule := range capsules {
		ref, err := capsule.reference()
		require.NoError(t, err)
		result[capsule.Generation.Manifest.GenerationID] = capacityCapsuleIdentity{Manifest: ref.ManifestSHA256, Recovery: ref.InputsSHA256, Payload: capsule.Generation.Manifest.Payload.Checksum}
	}
	return result
}

func capacityProducerBatchEvidence(t *testing.T, compiled *CompiledTopology, proof *TopologyMaterialization) []capacityBatchUsage {
	t.Helper()
	require.NoError(t, compiled.checkRetentionComparisons())
	usages := map[runtime.CatalogRetentionEntry]runtime.CatalogRetentionUsage{}
	for index, capsule := range compiled.inventory.Capsules {
		ref, err := capsule.reference()
		require.NoError(t, err)
		entry := runtime.CatalogRetentionEntry{ManifestSHA256: ref.ManifestSHA256, InputsSHA256: ref.InputsSHA256, SourceOrigin: runtime.CatalogRetentionOrigin(capsule.SourceOrigin), SourceDescriptorSHA256: ref.SourceDescriptorSHA256}
		usages[entry] = compiled.retentionComparisons[index].Usage()
	}
	result := make([]capacityBatchUsage, len(proof.receipts))
	for index, receipt := range proof.receipts {
		for _, record := range receipt.Records {
			usage, found := usages[record.Entry]
			require.True(t, found)
			result[index].Inputs++
			result[index].Raw += usage.RawBytes()
			result[index].Decoded += usage.DecodedBytes()
		}
		require.LessOrEqual(t, result[index].Inputs, runtime.MaxCatalogRetentionBatchInputs)
		require.LessOrEqual(t, result[index].Raw, int64(runtime.MaxCatalogRetentionBatchBytes))
		require.LessOrEqual(t, result[index].Decoded, int64(runtime.MaxCatalogRetentionBatchBytes))
	}
	return result
}

func capacityApplyWithLostReply(t *testing.T, compiled *CompiledTopology, target *capacityGuardedTarget, proof *TopologyMaterialization) int {
	t.Helper()
	require.Positive(t, compiled.StageCount())
	retries := 0
	for index := range compiled.StageCount() {
		lost := index == compiled.StageCount()/2
		target.native.failAfter = lost
		err := compiled.ApplyStage(t.Context(), index, target)
		if lost {
			require.ErrorContains(t, err, "lost committed native import reply")
			position := target.native.position
			require.NoError(t, compiled.ApplyStage(t.Context(), index, target))
			require.Equal(t, position, target.native.position)
			retries++
		} else {
			require.NoError(t, err)
		}
	}
	require.NoError(t, compiled.ApplySelection(t.Context(), proof, target))
	require.NoError(t, compiled.CheckSelection(t.Context(), proof, target))
	require.Equal(t, int64(compiled.StageCount()+1), target.native.position.Sequence)
	require.Len(t, target.stages, compiled.StageCount()+1)
	require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target.native.kv), storage.ErrImportRestricted)
	require.ErrorIs(t, target.backup.db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	current, err := target.witness.Current(t.Context(), target.guard.Boundary.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, target.guard.Boundary, current)
	return retries
}

func capacityBackupCompletedComponent(t *testing.T, target *capacityGuardedTarget, decision string) *capacityBackup {
	t.Helper()
	// Release only the component import barriers to capture the next original backup.
	// The SQL witness stays closed. This procedure approves no gateway, budget, or catalog authority.
	require.NoError(t, target.native.transfer.(storage.ImportReplayActivator).ActivateImportAt(t.Context(), target.native.claim, target.native.position, decision))
	require.NoError(t, target.backup.db.ActivateRelationalImportAt(t.Context(), target.guard.Snapshot, target.guard.Import, target.guard.Position, decision, func(context.Context, *sql.Conn) error { return nil }))
	boundary, err := target.witness.Close(t.Context(), target.guard.Boundary)
	require.NoError(t, err)
	require.False(t, boundary.Open)
	return capacityCaptureBackup(t, target.backup, capacityRecordTransfer(t, target.native.kv), boundary, filepath.Join(target.backup.config.EffectivePaths().ConfigDir, "completed-backup"))
}

func TestRecoveryCatalogMaximumSerializedInventoryRoundTrip(t *testing.T) {
	var selected []TopologyCapsule
	var acceptedID, candidateID string
	calibration := topologyRequest(TopologyLocalToFleet)
	calibration.DestinationIdentity = runtime.FleetIdentity{DeploymentID: "source", RecoveryEpoch: 2, BackendID: strings.Repeat("b", 64)}
	calibration.DestinationBoundary = recovery.Record{DeploymentID: "source", Epoch: 2, BackendID: strings.Repeat("b", 64)}
	source := capacityOriginalBackupWith(t, func(connected *Runtime, _ storage.KVStore, settings Settings, accepted catalogs.Generation) {
		candidate := capacityPublishCandidate(t, connected)
		acceptedID, candidateID = accepted.Manifest.GenerationID, candidate.Manifest.GenerationID
		require.NotEqual(t, acceptedID, candidateID)
		selected = []TopologyCapsule{capacitySelectedCapsule(t, settings, accepted), capacitySelectedCapsule(t, settings, candidate)}
	}, func(kv storage.KVStore, settings Settings, accepted catalogs.Generation) {
		var smallBytes int64
		for _, capsule := range selected {
			smallBytes += capacitySnapshotSize(t, capsule, calibration)
		}
		goal := (int64(fleetRetentionMaxBytes) - (4 << 20) - smallBytes) / 94
		padding := 17 << 20
		for range 3 {
			probe := capacityLargeHistoricalCapsule(t, "capacity-history-00", padding)
			size := capacitySnapshotSize(t, probe, calibration)
			padding += int((goal - size) * 3 / 4)
		}
		require.Positive(t, padding)
		capsules := make([]TopologyCapsule, 94)
		for index := range capsules {
			capsules[index] = capacityLargeHistoricalCapsule(t, fmt.Sprintf("capacity-history-%02d", index), padding)
		}
		capacityRetainOriginalCapsules(t, kv, settings, accepted, capsules)
	})
	forward := capacityImportTarget(t, source, "maximum-forward")
	request := capacityTransferRequest(forward, TopologyLocalToFleet, "maximum-forward")
	compiled, err := CompileRecoveryTopology(t.Context(), source.source, request)
	require.NoError(t, err)
	require.Len(t, compiled.inventory.Capsules, 96)
	expected := capacityOriginalIdentities(t, compiled.inventory.Capsules)
	evidence := capacityMaximumEvidence{Entries: 96, Accepted: acceptedID, Candidate: candidateID, OriginalManifest: source.source.ManifestDigest(), RollbackEntries: len(compiled.inventory.History)}
	require.Equal(t, catalogGenerationIndexCap, evidence.RollbackEntries)
	seal, err := compiled.Record()
	require.NoError(t, err)
	evidence.ForwardSealBytes = len(seal)
	require.LessOrEqual(t, len(seal), 64<<10)
	prefix := "catalog:fleet:{" + payloadDigest([]byte("source")) + "}:v1:"
	var inventory fleetInventory
	for _, mutation := range compiled.selection {
		if mutation.Key == prefix+"inventory" {
			require.NoError(t, json.Unmarshal(mutation.NewValue, &inventory))
		}
	}
	require.Len(t, inventory.Entries, 96)
	for _, entry := range inventory.Entries {
		evidence.SerializedBytes += int64(entry.Record.Size)
	}
	require.GreaterOrEqual(t, evidence.SerializedBytes, int64(fleetRetentionMaxBytes)-(8<<20))
	require.LessOrEqual(t, evidence.SerializedBytes, int64(fleetRetentionMaxBytes))
	target, options, err := forward.backup.settings.RecoveryTopologyTarget()
	require.NoError(t, err)
	proof, err := compiled.RetainAndMaterialize(t.Context(), target, options...)
	require.NoError(t, err)
	evidence.ProducerBatches = capacityProducerBatchEvidence(t, compiled, proof)
	rollback := append([]GenerationIndexEntry(nil), compiled.inventory.History...)
	evidence.LostReplyExactRetries += capacityApplyWithLostReply(t, compiled, forward, proof)
	evidence.ForwardStages = forward.stages
	fleetBackup := capacityBackupCompletedComponent(t, forward, compiled.Digest())
	evidence.FleetManifest = fleetBackup.source.ManifestDigest()
	// Reverse recovery models a later operation. Release the completed forward compiler's memory.
	compiled, proof = nil, nil
	goruntime.GC()
	reverse := capacityImportTarget(t, fleetBackup, "maximum-reverse")
	reverseRequest := capacityTransferRequest(reverse, TopologyFleetToLocal, "maximum-reverse")
	compiled, err = CompileRecoveryTopology(t.Context(), fleetBackup.source, reverseRequest)
	require.NoError(t, err)
	require.NotNil(t, compiled.inventory.Archive)
	require.Equal(t, expected, capacityOriginalIdentities(t, compiled.inventory.Capsules))
	require.Equal(t, rollback, compiled.inventory.History)
	seal, err = compiled.Record()
	require.NoError(t, err)
	evidence.ReverseSealBytes = len(seal)
	require.LessOrEqual(t, len(seal), 64<<10)
	target, options, err = reverse.backup.settings.RecoveryTopologyTarget()
	require.NoError(t, err)
	proof, err = compiled.RetainAndMaterialize(t.Context(), target, options...)
	require.NoError(t, err)
	evidence.ProducerBatches = append(evidence.ProducerBatches, capacityProducerBatchEvidence(t, compiled, proof)...)
	evidence.LostReplyExactRetries += capacityApplyWithLostReply(t, compiled, reverse, proof)
	evidence.ReverseStages = reverse.stages
	finalBackup := capacityBackupCompletedComponent(t, reverse, compiled.Digest())
	evidence.ReverseManifest = finalBackup.source.ManifestDigest()
	view, err := finalBackup.source.OpenCapturedKV(t.Context())
	require.NoError(t, err)
	require.NoError(t, InspectCapturedCatalog(t.Context(), view, finalBackup.source.CapturedBoundary()))
	accepted, err := capturedCatalog{view}.read(t.Context(), catalogCurrentGenerationKey, 4096)
	require.NoError(t, err)
	candidate, err := capturedCatalog{view}.read(t.Context(), candidateCurrentGenerationKey, 4096)
	require.NoError(t, err)
	require.Equal(t, acceptedID, string(accepted))
	require.Equal(t, candidateID, string(candidate))
	require.NoError(t, view.Close())
	require.Equal(t, 2, evidence.LostReplyExactRetries)
	body, err := json.Marshal(evidence, json.Deterministic(true))
	require.NoError(t, err)
	t.Log("CATALOG_MAXIMUM_EVIDENCE " + string(body))
}
