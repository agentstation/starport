package catalog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestPreparedCatalogInspectionRequiresOriginalBackup(t *testing.T) {
	inventory, view, _ := topologyLocalFixture(t)
	compiled, err := CompileTopologyTransfer(t.Context(), inventory, topologyRequest(TopologyLocalToFleet), view)
	require.NoError(t, err)
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), view, inventory.data.Boundary), recovery.ErrConflict)
	var missing *CompiledTopology
	require.ErrorIs(t, missing.InspectPreparedCapturedCatalog(t.Context(), view, inventory.data.Boundary), recovery.ErrConflict)
}

func TestPreparedFleetIdentityRefusesInvalidBoundaryEpoch(t *testing.T) {
	identity := runtime.FleetIdentity{DeploymentID: "destination", RecoveryEpoch: 2, BackendID: "verified-native-incarnation"}
	for _, epoch := range []int64{-1, 0, 1, 2, 3} {
		captured := capturedFleet{boundary: recovery.Record{DeploymentID: identity.DeploymentID, Epoch: epoch}, preparedIdentity: identity}
		if epoch == 2 {
			require.NoError(t, captured.validateIdentity(identity))
		} else {
			require.ErrorIs(t, captured.validateIdentity(identity), recovery.ErrConflict)
		}
	}
}

func TestPreparedCatalogInspectionClosedBoundaryAndFutureIdentity(t *testing.T) {
	f := recoveryTopologyBackupFixture(t)
	request := topologyRequest(TopologyLocalToFleet)
	request.DestinationBoundary = f.source.CapturedBoundary()
	request.DestinationIdentity = runtime.FleetIdentity{DeploymentID: request.DestinationBoundary.DeploymentID, RecoveryEpoch: uint64(request.DestinationBoundary.Epoch), BackendID: "verified-future-native-incarnation"}
	compiled, err := CompileRecoveryTopology(t.Context(), f.source, request)
	require.NoError(t, err)
	view, err := f.source.OpenCapturedKV(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Close()) })
	// The same callback verifies the unchanged source and the later final graph.
	// Their closed SQL boundaries are equal. Only the final graph has this transfer marker.
	require.NoError(t, compiled.InspectPreparedCapturedCatalog(t.Context(), view, f.source.CapturedBoundary()))
	kv, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, kv.Close()) })
	transfer, err := storage.OpenRecordTransfer(t.Context(), kv, "")
	require.NoError(t, err)
	target := &topologyImportTestTarget{kv: kv, transfer: transfer, claim: []byte("sealed-prepared-catalog-import"), completed: map[string]storage.ImportReplayPosition{}}
	require.NoError(t, transfer.Claim(t.Context(), target.claim))
	require.NoError(t, view.Enumerate(t.Context(), func(record storage.TransferRecord) error { return transfer.Import(t.Context(), target.claim, record) }))
	topologyApplyStages(t, compiled, target)
	proof := &TopologyMaterialization{topology: compiled.digest, digest: strings.Repeat("e", 64)}
	require.NoError(t, compiled.ApplySelection(t.Context(), proof, target))
	final := target.captured(t)
	require.Error(t, InspectCapturedCatalog(t.Context(), final, request.DestinationBoundary))
	require.NoError(t, compiled.InspectPreparedCapturedCatalog(t.Context(), final, request.DestinationBoundary))
	// An extra valid chunk passes ordinary semantic validation but was never part of this sealed transfer.
	extra := []byte("unselected but structurally valid catalog chunk")
	extraKey := catalogGenerationChunkKeyPrefix + payloadDigest(extra)
	require.NoError(t, target.kv.Set(t.Context(), extraKey, extra))
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), target.captured(t), request.DestinationBoundary), recovery.ErrConflict)
	require.NoError(t, target.kv.Delete(t.Context(), extraKey))
	// Original local generation descriptors survive this transfer unchanged.
	// Their absence, replacement, or new expiry must also fail the complete final census.
	checkedOriginal := false
	for key := range compiled.preparedCensus {
		if !strings.HasPrefix(key, catalogGenerationKeyPrefix) {
			continue
		}
		original, err := target.kv.Get(t.Context(), key)
		require.NoError(t, err)
		checkedOriginal = true
		for _, change := range []func() error{
			func() error { return target.kv.Delete(t.Context(), key) },
			func() error { return target.kv.Set(t.Context(), key, []byte("substituted-original-descriptor")) },
			func() error { return target.kv.SetWithTTL(t.Context(), key, original, time.Hour) },
		} {
			require.NoError(t, change())
			require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), target.captured(t), request.DestinationBoundary), recovery.ErrConflict)
			require.NoError(t, target.kv.Set(t.Context(), key, original))
		}
		break
	}
	require.True(t, checkedOriginal, "the fixture must retain an original local generation descriptor")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(ctx, final, request.DestinationBoundary), context.Canceled)
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(nil, final, request.DestinationBoundary), recovery.ErrConflict)
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), nil, request.DestinationBoundary), recovery.ErrConflict)
	wrong := request.DestinationBoundary
	wrong.BackendID = request.DestinationIdentity.BackendID
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), final, wrong), recovery.ErrConflict)
	wrong = request.DestinationBoundary
	wrong.Epoch++
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), final, wrong), recovery.ErrConflict)
	wrong = request.DestinationBoundary
	wrong.Open = true
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), final, wrong), recovery.ErrConflict)
	require.NoError(t, target.kv.Set(t.Context(), compiled.markerKey, []byte("changed-stage")))
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), target.captured(t), request.DestinationBoundary), recovery.ErrConflict)
	require.NoError(t, target.kv.Set(t.Context(), compiled.markerKey, compiled.marker))
	require.NoError(t, target.kv.Set(t.Context(), catalogCurrentGenerationKey, []byte("changed-selection")))
	require.ErrorIs(t, compiled.InspectPreparedCapturedCatalog(t.Context(), target.captured(t), request.DestinationBoundary), recovery.ErrConflict)
	current, err := f.witness.Current(t.Context(), f.boundary.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, f.boundary, current)
}
