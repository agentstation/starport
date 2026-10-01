package catalog

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestTopologySameBackendLocalPreservesAcceptedCandidate(t *testing.T) {
	inventory, source, _ := topologyLocalFixture(t)
	request := topologyRequest(TopologyFleetToLocal)
	request.Direction = "local-restore"
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, request, source)
	require.NoError(t, err)
	require.Empty(t, compiled.inventory.Archive)
	selection := map[string][]byte{}
	for _, mutation := range compiled.selection {
		selection[mutation.Key] = mutation.NewValue
	}
	require.Equal(t, []byte("accepted"), selection[catalogCurrentGenerationKey])
	require.Equal(t, []byte("candidate"), selection[candidateCurrentGenerationKey])
	for _, stage := range compiled.stages {
		for _, mutation := range stage.mutations {
			require.False(t, strings.HasPrefix(mutation.Key, "catalog:fleet:"))
		}
	}
}

func TestTopologySameBackendFleetRebindsAndArchivesOriginalAuthority(t *testing.T) {
	local, _, _ := topologyLocalFixture(t)
	kv := openInMemoryBadger(t)
	forward, err := CompileTopologyTransfer(t.Context(), local, topologyRequest(TopologyLocalToFleet), capturedCatalogView(t, kv))
	require.NoError(t, err)
	target := &topologyNativeTestTarget{kv: kv, completed: map[string]bool{}}
	topologyApplyStages(t, forward, target)
	require.NoError(t, forward.ApplySelection(t.Context(), &TopologyMaterialization{topology: forward.digest, digest: strings.Repeat("d", 64)}, target))
	prefix := "catalog:fleet:{" + payloadDigest([]byte("destination")) + "}:v1:"
	require.NoError(t, kv.SetWithTTL(t.Context(), prefix+"maintenance", []byte("originalMaintenance1234567890"), time.Hour))
	grant, err := json.Marshal(fleetGrant{Holder: "original-holder", Session: "original-session", Epoch: 1, Identity: forward.request.DestinationIdentity})
	require.NoError(t, err)
	require.NoError(t, kv.SetWithTTL(t.Context(), prefix+"lease", grant, time.Hour))
	boundary := forward.request.DestinationBoundary
	boundary.Epoch++
	captured := capturedCatalog{capturedCatalogView(t, kv)}
	archive, err := captureFleetHistoricalArchive(t.Context(), captured.records, boundary)
	require.NoError(t, err)
	census := &topologyBackupCensus{}
	source, err := census.fleetInventory(t.Context(), captured.records.(*recovery.KVSnapshotView), boundary, archive)
	require.NoError(t, err)
	request := topologyRequest(TopologyLocalToFleet)
	request.Direction = "fleet-restore"
	request.Operation.ID = "same-fleet-restore"
	request.DestinationBoundary.Epoch = 4
	request.DestinationBoundary.BackendID = "new-native-incarnation"
	request.DestinationIdentity = runtime.FleetIdentity{DeploymentID: request.DestinationBoundary.DeploymentID, RecoveryEpoch: 4, BackendID: "new-native-incarnation"}
	restored, err := CompileTopologyTransfer(t.Context(), source, request, captured.records.(*recovery.KVSnapshotView))
	require.NoError(t, err)
	require.Equal(t, source.data.Archive, restored.inventory.Archive)
	native := topologyPersistentImportTarget(t, captured.records.(*recovery.KVSnapshotView))
	kv = native.kv
	topologyApplyStages(t, restored, native)
	require.NoError(t, restored.ApplySelection(t.Context(), &TopologyMaterialization{topology: restored.digest, digest: strings.Repeat("e", 64)}, native))
	closed := request.DestinationBoundary
	closed.Epoch++
	require.NoError(t, InspectCapturedCatalog(t.Context(), native.captured(t), closed))
	require.Equal(t, source.data.Archive.prefix(), prefix)
	require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), kv), storage.ErrImportRestricted)
	var accepted fleetAcceptance
	raw, err := kv.Get(t.Context(), prefix+string(OperationAccepted))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &accepted))
	require.Equal(t, "accepted", accepted.Head.GenerationID)
	require.Equal(t, request.DestinationIdentity, accepted.Head.Identity)
	var head runtime.FleetHead
	raw, err = kv.Get(t.Context(), prefix+fleetHeadKind)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &head))
	require.Equal(t, "candidate", head.GenerationID)
	require.Equal(t, request.DestinationIdentity, head.Identity)
	for _, entry := range source.data.Archive.Inventory.Entries {
		_, err = kv.Get(t.Context(), fleetPublicationKey(prefix, entry.Head))
		require.Error(t, err, "original live publication must retire after archival")
	}
	_, err = kv.Get(t.Context(), prefix+"lease")
	require.Error(t, err, "restore cannot invent a refresh lease")
	raw, err = kv.Get(t.Context(), prefix+"epoch")
	require.NoError(t, err)
	require.Equal(t, "1", string(raw))
}
