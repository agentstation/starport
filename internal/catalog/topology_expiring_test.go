package catalog

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func topologyExpiringFixture(t *testing.T, ttl time.Duration) (*CompiledTopology, *recovery.KVSnapshotView, string) {
	t.Helper()
	local, _, _ := topologyLocalFixture(t)
	sourceKV := openInMemoryBadger(t)
	forward, err := CompileTopologyTransfer(t.Context(), local, topologyRequest(TopologyLocalToFleet), capturedCatalogView(t, sourceKV))
	require.NoError(t, err)
	target := &topologyNativeTestTarget{kv: sourceKV, completed: map[string]bool{}}
	topologyApplyStages(t, forward, target)
	require.NoError(t, forward.ApplySelection(t.Context(), &TopologyMaterialization{topology: forward.digest, digest: strings.Repeat("d", 64)}, target))
	prefix := "catalog:fleet:{" + payloadDigest([]byte("destination")) + "}:v1:"
	maintenance := []byte("originalMaintenance1234567890")
	require.NoError(t, sourceKV.SetWithTTL(t.Context(), prefix+"maintenance", maintenance, ttl))
	boundary := forward.request.DestinationBoundary
	boundary.Epoch++
	view := capturedCatalogView(t, sourceKV)
	archive, err := captureFleetHistoricalArchive(t.Context(), view, boundary)
	require.NoError(t, err)
	source, err := (&topologyBackupCensus{}).fleetInventory(t.Context(), view, boundary, archive)
	require.NoError(t, err)
	request := topologyRequest(TopologyFleetToLocal)
	request.Operation.ID = "original-expiring-retirement"
	compiled, err := CompileTopologyTransfer(t.Context(), source, request, view)
	require.NoError(t, err)
	return compiled, view, prefix
}

func TestTopologyExpiringSourceControlsUseDistinctSealedStages(t *testing.T) {
	compiled, view, prefix := topologyExpiringFixture(t, time.Hour)
	expiringIndex := -1
	for index, stage := range compiled.stages {
		if len(stage.expiring) != 0 {
			require.Equal(t, -1, expiringIndex)
			expiringIndex = index
			require.Empty(t, stage.mutations)
			require.Len(t, stage.expiring, 1)
		}
		for _, mutation := range stage.mutations {
			require.NotEqual(t, prefix+"maintenance", mutation.Key, "ordinary persistent CAS must never receive captured expiring controls")
		}
	}
	require.GreaterOrEqual(t, expiringIndex, 0)
	original := compiled.stages[expiringIndex].expiring[0]
	captured, err := view.ReadCaptured(t.Context(), prefix+"maintenance", topologyBatchMaxBytes)
	require.NoError(t, err)
	require.Equal(t, captured, original)
	require.Empty(t, compiled.stages[expiringIndex+1].expiring)
	require.Equal(t, compiled.markerKey, compiled.stages[expiringIndex+1].mutations[len(compiled.stages[expiringIndex+1].mutations)-1].Key)
	unsupported := &topologyNativeTestTarget{completed: map[string]bool{}}
	require.ErrorContains(t, compiled.ApplyStage(t.Context(), expiringIndex, unsupported), "native expiring-record owner")
	probe := &topologyExpiringDispatchProbe{}
	require.ErrorIs(t, compiled.ApplyStage(t.Context(), expiringIndex, probe), errExpiringDispatchProbe)
	require.Equal(t, compiled.TopologyDigest(), probe.topology)
	require.Equal(t, expiringIndex, probe.index)
	require.Equal(t, original, compiled.stages[expiringIndex].expiring[0], "target cannot mutate the sealed original record")
	changed := topologyBatch{expiring: cloneTopologyExpiring(compiled.stages[expiringIndex].expiring)}
	changed.expiring[0].ExpiresAtMillis++
	require.NotEqual(t, topologyStageDigest(compiled.stages[expiringIndex]), topologyStageDigest(changed))
}

var errExpiringDispatchProbe = errors.New("checked compiler dispatch without native effects")

type topologyExpiringDispatchProbe struct {
	topology string
	index    int
}

func (*topologyExpiringDispatchProbe) ReadCatalogTopology(context.Context, string, int) ([]byte, error) {
	return nil, storage.ErrNotFound
}
func (*topologyExpiringDispatchProbe) CompletedCatalogTopology(context.Context, string, int) (bool, error) {
	return false, nil
}
func (*topologyExpiringDispatchProbe) ApplyCatalogTopology(context.Context, string, int, []storage.CompareAndSwapMutation) error {
	return errors.New("expiring stage reached persistent CAS")
}
func (p *topologyExpiringDispatchProbe) ApplyExpiringCatalogTopology(_ context.Context, topology string, index int, records []storage.TransferRecord) error {
	p.topology, p.index = topology, index
	records[0].Value = bytes.Repeat([]byte("different"), 2)
	records[0].ExpiresAtMillis++
	return errExpiringDispatchProbe
}

func TestTopologyExpiringStagesKeepNativeBoundsAndOriginalExpiry(t *testing.T) {
	compiled := &CompiledTopology{expected: map[string][]byte{}}
	for index := 0; index < 129; index++ {
		require.NoError(t, compiled.appendExpiringRetirement(storage.TransferRecord{Key: strings.Repeat("k", index+1), Value: []byte("original"), ExpiresAtMillis: 1}))
	}
	require.Len(t, compiled.stages, 2)
	require.Len(t, compiled.stages[0].expiring, 128)
	for _, stage := range compiled.stages {
		require.Empty(t, stage.mutations)
		require.LessOrEqual(t, topologyExpiringBytes(stage.expiring), topologyBatchMaxBytes)
	}
	require.Error(t, compiled.appendExpiringRetirement(storage.TransferRecord{Key: "persistent", Value: []byte("bytes")}))
	require.ErrorIs(t, compiled.appendExpiringRetirement(storage.TransferRecord{Key: "large", Value: make([]byte, topologyBatchMaxBytes), ExpiresAtMillis: 1}), storage.ErrValueTooLarge)
}

func TestTopologyExpiringStageFingerprintPreservesValuePresence(t *testing.T) {
	absent := topologyBatch{expiring: []storage.TransferRecord{{Key: "catalog:fleet:{captured}:v1:lease", ExpiresAtMillis: 1}}}
	empty := topologyBatch{expiring: cloneTopologyExpiring(absent.expiring)}
	empty.expiring[0].Value = []byte{}
	require.NotEqual(t, topologyStageDigest(absent), topologyStageDigest(empty))
	require.Nil(t, cloneTopologyExpiring(absent.expiring)[0].Value)
	require.NotNil(t, cloneTopologyExpiring(empty.expiring)[0].Value)
}
