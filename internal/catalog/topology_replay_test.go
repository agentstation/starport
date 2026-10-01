package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func topologySmallCapsule(t *testing.T, id string) TopologyCapsule {
	t.Helper()
	generation := runtimeTestGeneration(t, id, testEmptyCatalog(t, catalogs.ProviderID(id)), time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC))
	raw, err := json.Marshal(generation.Manifest, json.Deterministic(true))
	require.NoError(t, err)
	inputJSON, err := json.Marshal(struct {
		Version       int                 `json:"version"`
		Baseline      catalogs.Generation `json:"baseline"`
		PublisherID   string              `json:"publisher_id"`
		Compatibility string              `json:"compatibility"`
	}{2, generation, "fixture-source", strings.Repeat("c", 64)}, json.Deterministic(true))
	require.NoError(t, err)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err = writer.Write(inputJSON)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	inputs := compressed.Bytes()
	return TopologyCapsule{Generation: generation, SourceOrigin: "no-descriptor", Recovery: runtime.CatalogRecovery{ManifestChecksum: payloadDigest(raw), Inputs: runtime.FleetRecovery{GenerationID: id, PayloadChecksum: generation.Manifest.Payload.Checksum, Data: inputs, Checksum: payloadDigest(inputs)}}}
}

func topologyLocalFixture(t *testing.T) (*TopologyInventory, *recovery.KVSnapshotView, []TopologyCapsule) {
	t.Helper()
	kv := openInMemoryBadger(t)
	accepted, err := NewGenerationStore(kv)
	require.NoError(t, err)
	candidate, err := newCandidateGenerationStore(kv)
	require.NoError(t, err)
	capsules := []TopologyCapsule{topologySmallCapsule(t, "rollback"), topologySmallCapsule(t, "accepted"), topologySmallCapsule(t, "candidate")}
	require.NoError(t, accepted.Commit(t.Context(), capsules[0].Generation, ""))
	require.NoError(t, accepted.Commit(t.Context(), capsules[1].Generation, "rollback"))
	require.NoError(t, candidate.Commit(t.Context(), capsules[2].Generation, ""))
	refs := make([]TopologyReference, len(capsules))
	for index, capsule := range capsules {
		refs[index], err = capsule.reference()
		require.NoError(t, err)
	}
	selection := TopologySelection{Accepted: refs[1], Candidate: refs[2], CapsuleInventory: refs}
	view := capturedCatalogView(t, kv)
	inventory, err := CaptureTopologyInventory(t.Context(), view, recovery.Record{DeploymentID: "source", Epoch: 1}, selection, capsules)
	require.NoError(t, err)
	return inventory, view, capsules
}

func topologyRequest(direction TopologyDirection) TopologyTransferRequest {
	request := TopologyTransferRequest{Direction: direction, Operation: recovery.RestoreOperation{ID: "transfer-test", FencingEvidence: "independent-writer-fence"}, DestinationBoundary: recovery.Record{DeploymentID: "destination", Epoch: 2, BackendID: "destination-backend"}, AcceptedDecisionSHA256: strings.Repeat("a", 64), ClosedImportSHA256: strings.Repeat("b", 64)}
	if direction == TopologyLocalToFleet {
		request.DestinationIdentity = runtime.FleetIdentity{DeploymentID: "destination", RecoveryEpoch: 2, BackendID: "destination-backend"}
	}
	return request
}

// This adapter tests compiler transactions on native stores. It does not qualify
// the separate recovery SQL/import guard, which owns production admission closure.
type topologyNativeTestTarget struct {
	kv        storage.KVStore
	completed map[string]bool
	calls     int
	failAfter bool
}

func (t *topologyNativeTestTarget) ReadCatalogTopology(ctx context.Context, key string, limit int) ([]byte, error) {
	value, err := t.kv.Get(ctx, key)
	if err == nil && len(value) > limit {
		return nil, storage.ErrValueTooLarge
	}
	return value, err
}
func (t *topologyNativeTestTarget) CompletedCatalogTopology(_ context.Context, digest string, batch int) (bool, error) {
	return t.completed[fmt.Sprint(digest, "/", batch)], nil
}
func (t *topologyNativeTestTarget) ApplyCatalogTopology(ctx context.Context, digest string, batch int, mutations []storage.CompareAndSwapMutation) error {
	id := fmt.Sprint(digest, "/", batch)
	if t.completed[id] {
		return nil
	}
	if len(mutations) > topologyBatchMaxMutations || batchBytes(mutations) > topologyBatchMaxBytes {
		return storage.ErrValueTooLarge
	}
	if err := t.kv.CompareAndSwapBatch(ctx, mutations); err != nil {
		return err
	}
	t.completed[id] = true
	t.calls++
	if t.failAfter {
		t.failAfter = false
		return errors.New("lost committed native reply")
	}
	return nil
}

func topologyApplyStages(t *testing.T, compiled *CompiledTopology, target TopologyTarget) {
	t.Helper()
	for index := 0; index < compiled.StageCount(); index++ {
		require.NoError(t, compiled.ApplyStage(t.Context(), index, target))
	}
}

func TestTopologyCompilerLocalToFleetPreservesSelectionsAndFencing(t *testing.T) {
	inventory, source, capsules := topologyLocalFixture(t)
	targetKV := openInMemoryBadger(t)
	targetView := capturedCatalogView(t, targetKV)
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), targetView)
	require.NoError(t, err)
	again, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), targetView)
	require.NoError(t, err)
	require.Equal(t, compiled.digest, again.digest)
	require.Equal(t, compiled.stages, again.stages)
	target := &topologyNativeTestTarget{kv: targetKV, completed: map[string]bool{}}
	topologyApplyStages(t, compiled, target)
	// A nil or unrelated materialization proof cannot select staged content.
	require.Error(t, compiled.ApplySelection(t.Context(), nil, target))
	require.Error(t, compiled.ApplySelection(t.Context(), &TopologyMaterialization{topology: "other"}, target))
	require.NoError(t, compiled.ApplySelection(t.Context(), &TopologyMaterialization{topology: compiled.digest, digest: strings.Repeat("c", 64)}, target))
	view := capturedCatalogView(t, targetKV)
	backupBoundary := compiled.request.DestinationBoundary
	backupBoundary.Epoch++
	require.NoError(t, InspectCapturedCatalog(t.Context(), view, backupBoundary))
	prefix := "catalog:fleet:{" + payloadDigest([]byte("destination")) + "}:v1:"
	require.ErrorIs(t, func() error { _, err := targetKV.Get(t.Context(), prefix+"lease"); return err }(), storage.ErrNotFound)
	epoch, err := targetKV.Get(t.Context(), prefix+"epoch")
	require.NoError(t, err)
	require.Equal(t, "1", string(epoch))
	var accepted fleetAcceptance
	raw, err := targetKV.Get(t.Context(), prefix+"accepted")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &accepted))
	var candidate runtime.FleetHead
	raw, err = targetKV.Get(t.Context(), prefix+"head")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &candidate))
	require.Equal(t, "accepted", accepted.Head.GenerationID)
	require.Equal(t, "candidate", candidate.GenerationID)
	require.Equal(t, inventory.data.History, accepted.History)
	var retained fleetInventory
	raw, err = targetKV.Get(t.Context(), prefix+"inventory")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &retained))
	for _, blob := range retained.Entries {
		data, err := readFleetBlob(t.Context(), prefix, blob, func(ctx context.Context, key string, _ int) ([]byte, error) { return targetKV.Get(ctx, key) })
		require.NoError(t, err)
		snapshot, err := decodeFleetBlob(data, blob)
		require.NoError(t, err)
		require.Zero(t, snapshot.Publication.Grant)
		require.NotNil(t, snapshot.Publication.RecoveryOrigin)
		require.Error(t, snapshot.Publication.ValidateRefreshPublication())
		require.True(t, hasTopologyRecovery(capsules, snapshot))
	}
	require.NoError(t, InspectCapturedCatalog(t.Context(), source, inventory.data.Boundary))
}

func TestTopologyCompilerRefusesChangedStagingAndRecoversLostReply(t *testing.T) {
	inventory, _, _ := topologyLocalFixture(t)
	kv := openInMemoryBadger(t)
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), capturedCatalogView(t, kv))
	require.NoError(t, err)
	target := &topologyNativeTestTarget{kv: kv, completed: map[string]bool{}, failAfter: true}
	require.ErrorContains(t, compiled.ApplyStage(t.Context(), 0, target), "lost committed")
	require.NoError(t, compiled.ApplyStage(t.Context(), 0, target))
	require.Equal(t, 1, target.calls)
	for index := 1; index < compiled.StageCount(); index++ {
		require.NoError(t, compiled.ApplyStage(t.Context(), index, target))
	}
	var changed string
	for key, value := range compiled.expected {
		if value != nil {
			changed = key
			break
		}
	}
	original, err := kv.Get(t.Context(), changed)
	require.NoError(t, err)
	require.NoError(t, kv.Set(t.Context(), changed, []byte("changed")))
	proof := &TopologyMaterialization{topology: compiled.digest, digest: strings.Repeat("c", 64)}
	require.ErrorContains(t, compiled.ApplySelection(t.Context(), proof, target), "staged catalog content changed")
	require.NoError(t, kv.Set(t.Context(), changed, original))
	require.NoError(t, compiled.ApplySelection(t.Context(), proof, target))
	prefix := "catalog:fleet:{" + payloadDigest([]byte("destination")) + "}:v1:"
	require.NoError(t, kv.Set(t.Context(), prefix+"head", []byte("later independent selection")))
	require.NoError(t, compiled.ApplySelection(t.Context(), proof, target))
	current, err := kv.Get(t.Context(), prefix+"head")
	require.NoError(t, err)
	require.Equal(t, "later independent selection", string(current))
	require.Error(t, compiled.CheckSelection(t.Context(), proof, target))
}

func TestTopologyInventoryRefusesMissingAndConflictingOriginalEvidence(t *testing.T) {
	inventory, view, capsules := topologyLocalFixture(t)
	for _, mode := range []string{"missing", "reordered", "wrong-manifest", "wrong-pointer", "duplicate", "unsupported-origin"} {
		t.Run(mode, func(t *testing.T) {
			raw, err := json.Marshal(capsules)
			require.NoError(t, err)
			var changed []TopologyCapsule
			require.NoError(t, json.Unmarshal(raw, &changed))
			selection := inventory.data.Selection
			selection.CapsuleInventory = append([]TopologyReference(nil), selection.CapsuleInventory...)
			switch mode {
			case "missing":
				changed = changed[1:]
			case "reordered":
				changed[0], changed[1] = changed[1], changed[0]
			case "wrong-manifest":
				changed[0].Generation.Manifest.GenerationID = "another"
			case "wrong-pointer":
				selection.Accepted = selection.Candidate
			case "duplicate":
				changed[0] = changed[1]
			case "unsupported-origin":
				changed[0].SourceOrigin = "directory-now"
			}
			_, err = CaptureTopologyInventory(t.Context(), view, inventory.data.Boundary, selection, changed)
			require.Error(t, err)
		})
	}
}

func TestTopologyCompilerBoundsAndPrivateImmutableClaims(t *testing.T) {
	inventory, _, capsules := topologyLocalFixture(t)
	original := inventory.digest
	capsules[0].Recovery.Inputs.Data[0] = 'x'
	require.Equal(t, original, inventory.digest)
	require.NotEqual(t, capsules[0].Recovery.Inputs.Data, inventory.data.Capsules[0].Recovery.Inputs.Data)
	require.NotContains(t, fmt.Sprintf("%+v %p", inventory, inventory), "retained_input")
	c := &CompiledTopology{}
	for index := range 150 {
		require.NoError(t, c.appendStage(storage.CompareAndSwapMutation{Key: fmt.Sprint("catalog-key", index), NewValue: bytes.Repeat([]byte("x"), generationChunkSize)}))
	}
	require.Greater(t, len(c.stages), 1)
	for _, batch := range c.stages {
		require.LessOrEqual(t, len(batch.mutations)+1, 128)
		require.LessOrEqual(t, batchBytes(batch.mutations)+4096, 4<<20)
	}
	require.ErrorIs(t, c.appendStage(storage.CompareAndSwapMutation{Key: "oversized", NewValue: make([]byte, 4<<20)}), storage.ErrValueTooLarge)
}

func TestTopologyFleetToLocalPreservesHistoricalEvidenceAndRetiresAuthority(t *testing.T) {
	inventory, _, _ := topologyLocalFixture(t)
	fleetKV := openInMemoryBadger(t)
	forward, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), capturedCatalogView(t, fleetKV))
	require.NoError(t, err)
	fleetTarget := &topologyNativeTestTarget{kv: fleetKV, completed: map[string]bool{}}
	topologyApplyStages(t, forward, fleetTarget)
	require.NoError(t, forward.ApplySelection(t.Context(), &TopologyMaterialization{topology: forward.digest, digest: strings.Repeat("d", 64)}, fleetTarget))
	prefix := "catalog:fleet:{" + payloadDigest([]byte("destination")) + "}:v1:"
	var retained fleetInventory
	raw, err := fleetKV.Get(t.Context(), prefix+"inventory")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &retained))
	retained.Readers["historicalReader1234567890"] = retained.Entries[0].ID
	pending := retained.Entries[0]
	pending.ID = "historicalPending1234567890"
	pending.Head.Revision = 4
	retained.Pending = &pending
	raw, err = json.Marshal(retained)
	require.NoError(t, err)
	require.NoError(t, fleetKV.Set(t.Context(), prefix+"inventory", raw))
	maintenance := []byte("historicalMaintenance1234567890")
	require.NoError(t, fleetKV.SetWithTTL(t.Context(), prefix+"maintenance", maintenance, time.Hour))
	grant, err := json.Marshal(fleetGrant{Holder: "historical-holder", Session: "historical-session", Epoch: 1, Identity: forward.request.DestinationIdentity})
	require.NoError(t, err)
	require.NoError(t, fleetKV.SetWithTTL(t.Context(), prefix+"lease", grant, time.Hour))
	boundary := forward.request.DestinationBoundary
	boundary.Epoch++
	sourceView := capturedCatalogView(t, fleetKV)
	capsules := make([]TopologyCapsule, 0, len(retained.Entries))
	var selection TopologySelection
	for _, blob := range retained.Entries {
		payload, err := readFleetBlob(t.Context(), prefix, blob, func(ctx context.Context, key string, _ int) ([]byte, error) { return fleetKV.Get(ctx, key) })
		require.NoError(t, err)
		snapshot, err := decodeFleetBlob(payload, blob)
		require.NoError(t, err)
		recoveryInput, err := runtime.CaptureFleetCatalogRecovery(t.Context(), snapshot)
		require.NoError(t, err)
		descriptor, err := fleetKV.Get(t.Context(), fleetPublicationKey(prefix, blob.Head))
		require.NoError(t, err)
		capsule := TopologyCapsule{Generation: snapshot.Publication.Generation, Recovery: recoveryInput, SourceOrigin: "fleet-publication", SourceDescriptor: descriptor}
		ref, err := capsule.reference()
		require.NoError(t, err)
		selection.CapsuleInventory = append(selection.CapsuleInventory, ref)
		if blob.Head.GenerationID == "accepted" {
			selection.Accepted = ref
		}
		if blob.Head.GenerationID == "candidate" {
			selection.Candidate = ref
		}
		capsules = append(capsules, capsule)
	}
	sourceInventory, err := CaptureTopologyInventory(t.Context(), sourceView, boundary, selection, capsules)
	require.NoError(t, err)
	require.NotNil(t, sourceInventory.data.Archive.Inventory.Pending)
	// Destination imports the exact source catalog records first. Transfer then compiles owner changes.
	target := topologyPersistentImportTarget(t, sourceView)
	localKV := target.kv
	request := topologyRequest(TopologyFleetToLocal)
	request.Operation.ID = "reverse-transfer"
	request.DestinationBoundary.BackendID = ""
	reverse, err := CompileTopologyTransfer(t.Context(), sourceInventory, request, sourceView)
	require.NoError(t, err)
	topologyApplyStages(t, reverse, target)
	require.NoError(t, reverse.ApplySelection(t.Context(), &TopologyMaterialization{topology: reverse.digest, digest: strings.Repeat("e", 64)}, target))
	require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), localKV), storage.ErrImportRestricted)
	require.Equal(t, int64(reverse.StageCount()+1), target.position.Sequence)
	keys, err := localKV.ScanWithPrefix(t.Context(), "catalog:fleet:", 10000)
	require.NoError(t, err)
	require.Empty(t, keys)
	accepted, err := localKV.Get(t.Context(), catalogCurrentGenerationKey)
	require.NoError(t, err)
	require.Equal(t, "accepted", string(accepted))
	candidate, err := localKV.Get(t.Context(), candidateCurrentGenerationKey)
	require.NoError(t, err)
	require.Equal(t, "candidate", string(candidate))
	finalView := target.captured(t)
	require.NoError(t, InspectCapturedCatalog(t.Context(), finalView, request.DestinationBoundary))
	archive := sourceInventory.data.Archive
	require.Equal(t, forward.request.DestinationIdentity, archive.Identity)
	require.Equal(t, retained.Readers, archive.Inventory.Readers)
	require.NotNil(t, archive.Inventory.Pending)
	for _, record := range archive.Records {
		if record.Kind == "lease" {
			require.Equal(t, grant, record.Value)
			require.Positive(t, record.ExpiresAtMillis)
		}
		if record.Kind == "maintenance" {
			require.Equal(t, maintenance, record.Value)
		}
	}
	// An archive never follows destination identity. Corrupt historical bytes are still refused.
	var archiveChunk string
	for key := range reverse.expected {
		if strings.HasPrefix(key, topologyArchivePrefix+"chunk:") {
			archiveChunk = key
			break
		}
	}
	require.NotEmpty(t, archiveChunk)
	require.NoError(t, localKV.Set(t.Context(), archiveChunk, []byte("corrupt")))
	require.Error(t, InspectCapturedCatalog(t.Context(), target.captured(t), request.DestinationBoundary))
}

func TestTopologyCompilerRetainsComplete96Capsules(t *testing.T) {
	kv := openInMemoryBadger(t)
	accepted, err := NewGenerationStore(kv)
	require.NoError(t, err)
	candidate, err := newCandidateGenerationStore(kv)
	require.NoError(t, err)
	var capsules []TopologyCapsule
	var selection TopologySelection
	previous := ""
	for index := range 96 {
		capsule := topologySmallCapsule(t, fmt.Sprintf("generation-%02d", index))
		ref, err := capsule.reference()
		require.NoError(t, err)
		capsules = append(capsules, capsule)
		selection.CapsuleInventory = append(selection.CapsuleInventory, ref)
		if index < 95 {
			require.NoError(t, accepted.Commit(t.Context(), capsule.Generation, previous))
			previous = capsule.Generation.Manifest.GenerationID
		}
		if index == 94 {
			selection.Accepted = ref
		}
		if index == 95 {
			selection.Candidate = ref
			require.NoError(t, candidate.Commit(t.Context(), capsule.Generation, ""))
		}
	}
	inventory, err := CaptureTopologyInventory(t.Context(), capturedCatalogView(t, kv), recovery.Record{DeploymentID: "source", Epoch: 1}, selection, capsules)
	require.NoError(t, err)
	destination := openInMemoryBadger(t)
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), capturedCatalogView(t, destination))
	require.NoError(t, err)
	target := &topologyNativeTestTarget{kv: destination, completed: map[string]bool{}}
	topologyApplyStages(t, compiled, target)
	require.NoError(t, compiled.ApplySelection(t.Context(), &TopologyMaterialization{topology: compiled.digest, digest: strings.Repeat("f", 64)}, target))
	prefix := "catalog:fleet:{" + payloadDigest([]byte("destination")) + "}:v1:"
	raw, err := destination.Get(t.Context(), prefix+"inventory")
	require.NoError(t, err)
	var retained fleetInventory
	require.NoError(t, json.Unmarshal(raw, &retained))
	require.Len(t, retained.Entries, 96)
	require.Len(t, inventory.data.History, 32)
}

func TestTopologyFullEmbeddedRetainsAndMaterializesActualProducerEvidence(t *testing.T) {
	sourceKV := openInMemoryBadger(t)
	settings := identityTestSettings(filepath.Join(t.TempDir(), "source-state"), "", "127.0.0.1:0")
	settings.Values = map[string]string{catalogconfig.NetworkMode: "offline", catalogconfig.SchedulerIdentity: "source-scheduler"}
	source, err := OpenRuntime(t.Context(), sourceKV, settings, nil)
	require.NoError(t, err)
	candidate, err := source.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, source.Accept(t.Context(), candidate))
	generation, err := source.AcceptedGeneration(t.Context())
	require.NoError(t, err)
	require.NoError(t, source.Close(t.Context()))
	checksums, err := runtime.CatalogRecoveryChecksums(t.Context(), settings.StateDirectory, settings.directoryOwner(), "source-scheduler", generation)
	require.NoError(t, err)
	require.Len(t, checksums, 1)
	input, err := runtime.ReadCatalogRecovery(t.Context(), settings.StateDirectory, settings.directoryOwner(), "source-scheduler", generation, checksums[0])
	require.NoError(t, err)
	descriptor, err := os.ReadFile(filepath.Join(settings.StateDirectory, "catalog-runtime", "generation-inputs", input.ManifestChecksum+"-"+checksums[0]+".json.gz"))
	require.NoError(t, err)
	capsule := TopologyCapsule{Generation: generation, Recovery: input, SourceOrigin: "local-descriptor", SourceDescriptor: descriptor}
	ref, err := capsule.reference()
	require.NoError(t, err)
	inventory, err := CaptureTopologyInventory(t.Context(), capturedCatalogView(t, sourceKV), recovery.Record{DeploymentID: "source", Epoch: 1}, TopologySelection{Accepted: ref, Candidate: ref, CapsuleInventory: []TopologyReference{ref}}, []TopologyCapsule{capsule})
	require.NoError(t, err)
	destinationKV := openInMemoryBadger(t)
	destinationSettings := settings
	destinationSettings.StateDirectory = filepath.Join(t.TempDir(), "destination-state")
	destinationSettings.Values = map[string]string{catalogconfig.NetworkMode: "offline", catalogconfig.SchedulerIdentity: "destination-scheduler"}
	destination, err := OpenRuntime(t.Context(), destinationKV, destinationSettings, nil)
	require.NoError(t, err)
	require.NoError(t, destination.Close(t.Context()))
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), capturedCatalogView(t, destinationKV))
	require.NoError(t, err)
	target := &topologyNativeTestTarget{kv: destinationKV, completed: map[string]bool{}}
	topologyApplyStages(t, compiled, target)
	options, closeSource, err := fleetAdoptionOptions(t.Context(), destinationSettings)
	defer closeSource()
	require.NoError(t, err)
	runtimeTarget := TopologyRuntimeTarget{Directory: destinationSettings.StateDirectory, Owner: destinationSettings.directoryOwner(), SchedulerIdentity: "destination-scheduler"}
	proof, err := compiled.RetainAndMaterialize(t.Context(), runtimeTarget, options...)
	require.NoError(t, err)
	require.Len(t, proof.receipts, 1)
	require.Equal(t, ref.ManifestSHA256, proof.selected.SelectedManifestSHA256)
	require.Equal(t, ref.InputsSHA256, proof.selected.SelectedInputsSHA256)
	require.NotContains(t, fmt.Sprintf("%+v %p", proof, proof), ref.InputsSHA256)
	require.NoError(t, compiled.ApplySelection(t.Context(), proof, target))
	retry, err := compiled.RetainAndMaterialize(t.Context(), runtimeTarget, options...)
	require.NoError(t, err)
	require.Equal(t, proof.digest, retry.digest)
	require.NoError(t, compiled.ApplySelection(t.Context(), retry, target))
	backupBoundary := compiled.request.DestinationBoundary
	backupBoundary.Epoch++
	require.NoError(t, InspectCapturedCatalog(t.Context(), capturedCatalogView(t, destinationKV), backupBoundary))
	require.NoError(t, runtime.InspectRetainedDirectory(t.Context(), runtimeTarget.Directory, runtimeTarget.Owner, runtimeTarget.SchedulerIdentity))
	record, err := proof.Record()
	require.NoError(t, err)
	restored, err := compiled.InspectMaterialization(t.Context(), runtimeTarget, record, options...)
	require.NoError(t, err)
	require.Equal(t, proof.digest, restored.digest)
	require.NoError(t, compiled.CheckSelection(t.Context(), restored, target))
	changedRecord := append([]byte(nil), record...)
	changedRecord[len(changedRecord)/2] ^= 1
	_, err = compiled.InspectMaterialization(t.Context(), runtimeTarget, changedRecord, options...)
	require.Error(t, err)
	_, err = compiled.InspectMaterialization(t.Context(), runtimeTarget, record, append(options, runtime.WithAcquisitionSources())...)
	require.Error(t, err)
	baselinePath := filepath.Join(runtimeTarget.Directory, "catalog-runtime", "recovery-baseline.json")
	require.NoError(t, os.WriteFile(baselinePath, []byte("changed selected baseline"), 0600))
	_, err = compiled.InspectMaterialization(t.Context(), runtimeTarget, record, options...)
	require.Error(t, err)
	stillChanged, err := os.ReadFile(baselinePath)
	require.NoError(t, err)
	require.Equal(t, "changed selected baseline", string(stillChanged), "passive inspection must not repair changed evidence")

}

func TestTopologyNativeValkeyTransactionsPreserveClosedPostgresBoundary(t *testing.T) {
	inventory, _, _ := topologyLocalFixture(t)
	fleet, kv, witness := fleetTestStore(t)
	closed, err := witness.Close(t.Context(), fleet.approval)
	require.NoError(t, err)
	request := topologyRequest(TopologyLocalToFleet)
	request.DestinationBoundary = closed
	request.DestinationIdentity = runtime.FleetIdentity{DeploymentID: closed.DeploymentID, RecoveryEpoch: uint64(closed.Epoch), BackendID: closed.BackendID}
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, request, capturedCatalogView(t, kv))
	require.NoError(t, err)
	target := &topologyNativeTestTarget{kv: kv, completed: map[string]bool{}}
	topologyApplyStages(t, compiled, target)
	require.NoError(t, compiled.ApplySelection(t.Context(), &TopologyMaterialization{topology: compiled.digest, digest: strings.Repeat("d", 64)}, target))
	current, err := witness.Current(t.Context(), closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, closed, current)
	require.False(t, current.Open)
	// A later closed backup boundary validates these recovery-origin publications.
	backupBoundary := closed
	backupBoundary.Epoch++
	require.NoError(t, InspectCapturedCatalog(t.Context(), capturedCatalogView(t, kv), backupBoundary))
	_, err = NewFleetStore(t.Context(), kv.(storage.IncarnationProvider), witness, closed.DeploymentID)
	require.Error(t, err, "compilation and selection never open serving approval")
}

func TestTopologyArchiveRepresentationPreservesRawChunksAndCheckedBounds(t *testing.T) {
	capsule := topologySmallCapsule(t, "archive-record")
	record, chunks := encodeGenerationPayload(capsule.Recovery.Inputs.Data)
	historical := historicalFleetRecord{Kind: "blob", Identity: "original:identity", Value: capsule.Recovery.Inputs.Data, Record: record}
	encoded, err := json.Marshal(historical)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "value")
	require.Len(t, chunks, 1)
	for _, value := range chunks {
		require.Equal(t, capsule.Recovery.Inputs.Data, value)
	}
	exact, err := addHistoricalSourceBytes(fleetRetentionMaxBytes-100, 60, 40)
	require.NoError(t, err)
	require.EqualValues(t, fleetRetentionMaxBytes, exact)
	for _, input := range [][3]int64{{fleetRetentionMaxBytes - 100, 60, 41}, {fleetRetentionMaxBytes, 1, 0}, {-1, 1, 1}, {0, -1, 1}, {0, 1, -1}, {1 << 62, 1 << 62, 1 << 62}} {
		_, err = addHistoricalSourceBytes(input[0], input[1], input[2])
		require.ErrorIs(t, err, storage.ErrValueTooLarge)
	}
	archive := fleetHistoricalData{Boundary: recovery.Record{DeploymentID: "source", Epoch: 1}, Records: []historicalFleetRecord{historical}}
	archive.SourceBytes = int64(len(archive.prefix()+historical.Kind+":"+historical.Identity)) + int64(record.Size)
	require.NoError(t, archive.validateCensus())
	archive.SourceBytes++
	require.Error(t, archive.validateCensus())
	archive.Records[0].Record.Size = 1 << 62
	require.Error(t, archive.validateCensus())
}

func TestTopologyReplayChecksEveryDistinctSelection(t *testing.T) {
	accepted := TopologyReference{ManifestSHA256: strings.Repeat("a", 64)}
	candidate := TopologyReference{ManifestSHA256: strings.Repeat("b", 64)}
	compiled := &CompiledTopology{inventory: topologyInventoryData{Selection: TopologySelection{Accepted: accepted, Candidate: candidate}}}
	require.Equal(t, []TopologyReference{accepted, candidate}, compiled.replayReferences(TopologyReference{}))
	require.Equal(t, []TopologyReference{accepted}, compiled.replayReferences(candidate))
	require.Equal(t, []TopologyReference{candidate}, compiled.replayReferences(accepted))
	compiled.inventory.Selection.Candidate = accepted
	require.Equal(t, []TopologyReference{accepted}, compiled.replayReferences(TopologyReference{}))
	require.Empty(t, compiled.replayReferences(accepted))
}

func TestTopologyArchiveMetadataOverheadStaysWithinRepresentationBound(t *testing.T) {
	archive := fleetHistoricalData{Version: 1, Boundary: recovery.Record{DeploymentID: "source", Epoch: 1}}
	appendRecord := func(kind, identity string, value []byte) {
		record, _ := encodeGenerationPayload(value)
		archive.Records = append(archive.Records, historicalFleetRecord{Kind: kind, Identity: identity, Record: record})
		suffix := kind
		if identity != "" {
			suffix += ":" + identity
		}
		var err error
		archive.SourceBytes, err = addHistoricalSourceBytes(archive.SourceBytes, int64(len(archive.prefix()+suffix)), int64(len(value)))
		require.NoError(t, err)
	}
	// Tiny final chunks maximize descriptor overhead. Include the pending publication.
	for index := 0; index <= fleetRetentionMaxEntries; index++ {
		appendRecord("blob", payloadDigest([]byte(fmt.Sprint(index)))+":"+payloadDigest([]byte{1}), []byte{1})
	}
	for _, kind := range []string{fleetInventoryKind, "epoch", fleetHeadKind, string(OperationAccepted), fleetInventoryInitializedKind, fleetHeadInitializedKind, fleetMaintenanceKind, fleetLeaseKind} {
		appendRecord(kind, "", []byte{1})
	}
	encoded, err := json.Marshal(archive, json.Deterministic(true))
	require.NoError(t, err)
	allowance := int64(fleetRetentionRecordBytes + 2*fleetDescriptorMaxBytes + (fleetRetentionMaxEntries+9)*(1<<10))
	require.LessOrEqual(t, int64(len(encoded)), archive.SourceBytes+allowance)
	require.EqualValues(t, fleetRetentionMaxBytes+allowance, topologyArchiveMetadataMaxBytes)
	require.NotContains(t, string(encoded), "\"value\"")
}

func TestTopologyInventoryBoundsFinalCanonicalIdentity(t *testing.T) {
	inventory, source, capsules := topologyLocalFixture(t)
	initial, err := json.Marshal(topologyInventoryData{Boundary: inventory.data.Boundary, Selection: inventory.data.Selection, Capsules: capsules}, json.Deterministic(true))
	require.NoError(t, err)
	_, err = captureTopologyInventoryLimit(t.Context(), source, inventory.data.Boundary, inventory.data.Selection, capsules, int64(len(initial)))
	require.ErrorIs(t, err, storage.ErrValueTooLarge, "history must count against the final canonical identity budget")
}

func TestTopologyFleetSelectionRequiresExactSameGenerationPublication(t *testing.T) {
	localKV := openInMemoryBadger(t)
	original := topologySmallCapsule(t, "same-generation")
	other := original
	reader, err := gzip.NewReader(bytes.NewReader(original.Recovery.Inputs.Data))
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	data = bytes.ReplaceAll(data, []byte("fixture-source"), []byte("other-fixture-source"))
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err = writer.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	other.Recovery.Inputs.Data = compressed.Bytes()
	other.Recovery.Inputs.Checksum = payloadDigest(other.Recovery.Inputs.Data)
	originalRef, err := original.reference()
	require.NoError(t, err)
	otherRef, err := other.reference()
	require.NoError(t, err)
	require.NotEqual(t, originalRef, otherRef)
	accepted, err := NewGenerationStore(localKV)
	require.NoError(t, err)
	require.NoError(t, accepted.Commit(t.Context(), original.Generation, ""))
	candidate, err := newCandidateGenerationStore(localKV)
	require.NoError(t, err)
	require.NoError(t, candidate.Commit(t.Context(), other.Generation, ""))
	selection := TopologySelection{Accepted: originalRef, Candidate: otherRef, CapsuleInventory: []TopologyReference{originalRef, otherRef}}
	local, err := CaptureTopologyInventory(t.Context(), capturedCatalogView(t, localKV), recovery.Record{DeploymentID: "source", Epoch: 1}, selection, []TopologyCapsule{original, other})
	require.NoError(t, err)
	fleetKV := openInMemoryBadger(t)
	forward, err := CompileTopologyTransfer(t.Context(), local, topologyRequest(TopologyLocalToFleet), capturedCatalogView(t, fleetKV))
	require.NoError(t, err)
	target := &topologyNativeTestTarget{kv: fleetKV, completed: map[string]bool{}}
	topologyApplyStages(t, forward, target)
	require.NoError(t, forward.ApplySelection(t.Context(), &TopologyMaterialization{topology: forward.digest, digest: strings.Repeat("d", 64)}, target))
	prefix := "catalog:fleet:{" + payloadDigest([]byte("destination")) + "}:v1:"
	var retained fleetInventory
	raw, err := fleetKV.Get(t.Context(), prefix+"inventory")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &retained))
	var capsules []TopologyCapsule
	selection = TopologySelection{}
	for index, blob := range retained.Entries {
		payload, err := readFleetBlob(t.Context(), prefix, blob, func(ctx context.Context, key string, _ int) ([]byte, error) { return fleetKV.Get(ctx, key) })
		require.NoError(t, err)
		snapshot, err := decodeFleetBlob(payload, blob)
		require.NoError(t, err)
		recoveryInput, err := runtime.CaptureFleetCatalogRecovery(t.Context(), snapshot)
		require.NoError(t, err)
		descriptor, err := fleetKV.Get(t.Context(), fleetPublicationKey(prefix, blob.Head))
		require.NoError(t, err)
		capsule := TopologyCapsule{Generation: snapshot.Publication.Generation, Recovery: recoveryInput, SourceOrigin: "fleet-publication", SourceDescriptor: descriptor}
		ref, err := capsule.reference()
		require.NoError(t, err)
		selection.CapsuleInventory = append(selection.CapsuleInventory, ref)
		if index == 0 {
			selection.Accepted = ref
		} else {
			selection.Candidate = ref
		}
		capsules = append(capsules, capsule)
	}
	boundary := forward.request.DestinationBoundary
	boundary.Epoch++
	view := capturedCatalogView(t, fleetKV)
	fleetSource, err := CaptureTopologyInventory(t.Context(), view, boundary, selection, capsules)
	require.NoError(t, err)
	wrongDescriptor := selection.Accepted
	wrongDescriptor.SourceDescriptorSHA256 = selection.Candidate.SourceDescriptorSHA256
	require.Error(t, checkFleetSelectionReference(t.Context(), capturedCatalog{view}, &fleetSource.data, wrongDescriptor, fleetSource.data.Archive.Accepted.Head), "matching recovery checksum cannot substitute another original publication descriptor")
	for _, affected := range []string{"accepted", "candidate"} {
		t.Run(affected, func(t *testing.T) {
			changed := selection
			if affected == "accepted" {
				changed.Accepted = selection.Candidate
			} else {
				changed.Candidate = selection.Accepted
			}
			_, err := CaptureTopologyInventory(t.Context(), view, boundary, changed, capsules)
			require.Error(t, err, "same generation ID cannot substitute another publication's inputs or descriptor")
		})
	}
}

func TestTopologyInventoryStreamingIdentityMatchesCanonicalEncoding(t *testing.T) {
	inventory, _, _ := topologyLocalFixture(t)
	encoded, err := json.Marshal(inventory.data, json.Deterministic(true))
	require.NoError(t, err)
	digest, err := topologyIdentityDigest(inventory.data, int64(len(encoded)))
	require.NoError(t, err)
	require.Equal(t, payloadDigest(encoded), digest)
	_, err = topologyIdentityDigest(inventory.data, int64(len(encoded)-1))
	require.ErrorIs(t, err, storage.ErrValueTooLarge)
	_, err = topologyIdentityDigest(inventory.data, 0)
	require.ErrorIs(t, err, storage.ErrValueTooLarge)
	_, err = topologyIdentityDigest(inventory.data, -1)
	require.ErrorIs(t, err, storage.ErrValueTooLarge)
}

func TestTopologyCompilerKeepsPreparedSQLBoundaryAndSeparateFleetIdentity(t *testing.T) {
	inventory, _, _ := topologyLocalFixture(t)
	target := capturedCatalogView(t, openInMemoryBadger(t))
	request := topologyRequest(TopologyLocalToFleet)
	request.DestinationBoundary.BackendID = ""
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, request, target)
	require.NoError(t, err)
	require.Equal(t, request.DestinationBoundary, compiled.request.DestinationBoundary)
	require.Equal(t, "destination-backend", compiled.request.DestinationIdentity.BackendID)
	changed := request
	changed.DestinationIdentity.BackendID = "other-native-incarnation"
	other, err := CompileTopologyTransfer(t.Context(), inventory, changed, target)
	require.NoError(t, err)
	require.NotEqual(t, compiled.digest, other.digest)
	for _, mutate := range []func(*TopologyTransferRequest){
		func(r *TopologyTransferRequest) { r.DestinationBoundary.BackendID = "another-established-backend" },
		func(r *TopologyTransferRequest) { r.DestinationIdentity.BackendID = "" },
		func(r *TopologyTransferRequest) { r.DestinationIdentity.DeploymentID = "other" },
		func(r *TopologyTransferRequest) { r.DestinationIdentity.RecoveryEpoch++ },
		func(r *TopologyTransferRequest) { r.DestinationBoundary.Open = true },
	} {
		invalid := request
		mutate(&invalid)
		_, err := CompileTopologyTransfer(t.Context(), inventory, invalid, target)
		require.Error(t, err)
	}
}
