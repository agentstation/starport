package catalog

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// topologyFleetTestTarget applies compiled stages to the native store of one memory fleet.
// It tests the compiled transfer, not the recovery SQL/import guard.
type topologyFleetTestTarget struct {
	store     *memoryIncarnationStore
	completed map[string]bool
}

func (t *topologyFleetTestTarget) ReadCatalogTopology(ctx context.Context, key string, limit int) ([]byte, error) {
	value, _, err := t.store.ReadWithLifetime(ctx, key, limit)
	return value, err
}

func (t *topologyFleetTestTarget) CompletedCatalogTopology(_ context.Context, digest string, batch int) (bool, error) {
	return t.completed[fmt.Sprint(digest, "/", batch)], nil
}

func (t *topologyFleetTestTarget) ApplyCatalogTopology(ctx context.Context, digest string, batch int, mutations []storage.CompareAndSwapMutation) error {
	id := fmt.Sprint(digest, "/", batch)
	if t.completed[id] {
		return nil
	}
	if err := t.store.CompareAndSwap(ctx, mutations); err != nil {
		return err
	}
	t.completed[id] = true
	return nil
}

// TestLocalToSharedPromotionAfterMove promotes the packaged baseline over a head that a local-to-fleet move carried.
// The local source accepted a generation from an older baseline, as a deployment that an older binary
// served does. The move rewrites it as fleet revision 1 with a recovery origin, no grant, and no lease.
// Activation leaves no bootstrap grant, so the leader must open over the carried head without one.
func TestLocalToSharedPromotionAfterMove(t *testing.T) {
	ctx := t.Context()
	template, retained := olderBaselineFleet(t)
	templateSession := template.session()
	templateHead, err := templateSession.CurrentHead(ctx)
	require.NoError(t, err)
	published, err := templateSession.Publication(ctx, templateHead)
	require.NoError(t, err)
	older := published.Publication.Generation
	require.Equal(t, retained.GenerationID, older.Manifest.GenerationID)
	manifest, err := json.Marshal(older.Manifest, json.Deterministic(true))
	require.NoError(t, err)
	capsule := TopologyCapsule{Generation: older, SourceOrigin: "no-descriptor", Recovery: runtime.CatalogRecovery{ManifestChecksum: payloadDigest(manifest), Inputs: published.Publication.Recovery}}
	ref, err := capsule.reference()
	require.NoError(t, err)

	sourceKV := openInMemoryBadger(t)
	accepted, err := NewGenerationStore(sourceKV)
	require.NoError(t, err)
	candidate, err := newCandidateGenerationStore(sourceKV)
	require.NoError(t, err)
	require.NoError(t, accepted.Commit(ctx, older, ""))
	require.NoError(t, candidate.Commit(ctx, older, ""))
	deployment := "local-move-" + rand.Text()
	inventory, err := CaptureTopologyInventory(ctx, capturedCatalogView(t, sourceKV), recovery.Record{DeploymentID: deployment, Epoch: 1},
		TopologySelection{Accepted: ref, Candidate: ref, CapsuleInventory: []TopologyReference{ref}}, []TopologyCapsule{capsule})
	require.NoError(t, err)

	// Activation raises the epoch, binds the native backend, and opens the gate with bootstrap_allowed=0.
	boundary := recovery.Record{DeploymentID: deployment, Epoch: 2}
	approved := recovery.Record{DeploymentID: deployment, Epoch: 2, Open: true, BackendID: "memory-backend", Evidence: "local-to-shared-activation"}
	request := TopologyTransferRequest{
		Direction: TopologyLocalToFleet, DestinationBoundary: boundary,
		DestinationIdentity:    runtime.FleetIdentity{DeploymentID: deployment, RecoveryEpoch: uint64(boundary.Epoch), BackendID: approved.BackendID},
		Operation:              recovery.RestoreOperation{ID: "local-move", FencingEvidence: "independent-writer-fence"},
		AcceptedDecisionSHA256: strings.Repeat("a", 64), ClosedImportSHA256: strings.Repeat("b", 64),
	}
	compiled, err := CompileTopologyTransfer(ctx, inventory, request, capturedCatalogView(t, openInMemoryBadger(t)))
	require.NoError(t, err)
	fleet := &memoryFleet{store: newMemoryIncarnationStore(), witness: &memoryFleetWitness{record: approved, consumed: true}, kv: storage.NewMockStore()}
	target := &topologyFleetTestTarget{store: fleet.store, completed: map[string]bool{}}
	topologyApplyStages(t, compiled, target)
	require.NoError(t, compiled.ApplySelection(ctx, &TopologyMaterialization{topology: compiled.digest, digest: strings.Repeat("c", 64)}, target))

	session := fleet.session()
	carried, err := session.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, carried.Head.Revision)
	require.Equal(t, retained.GenerationID, carried.Head.GenerationID)
	require.NotNil(t, carried.Publication.RecoveryOrigin)
	require.Equal(t, "local-move", carried.Publication.RecoveryOrigin.OperationID)
	require.Zero(t, carried.Publication.Grant)
	_, _, err = fleet.store.ReadWithLifetime(ctx, session.prefix+"lease", 4096)
	require.ErrorIs(t, err, storage.ErrNotFound, "the move must not invent a refresh lease")

	leader := fleet.open(t)
	report, err := leader.BaselineReport()
	require.NoError(t, err)
	require.True(t, report.Promotable, report.Refusal)
	require.Equal(t, baselineIdentity(retained), report.Retained)
	require.NotEqual(t, report.Packaged.GenerationID, report.Retained.GenerationID)
	require.Equal(t, deployment, report.DeploymentID)
	// The leader takes the first lease and republishes the carried generation under its grant.
	require.EqualValues(t, 2, report.HeadRevision)
	opened, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	republication, err := leader.fleet.Publication(ctx, opened)
	require.NoError(t, err)
	require.Equal(t, carried.Head, republication.Publication.Expected)
	require.Equal(t, retained.GenerationID, opened.GenerationID)
	require.Nil(t, republication.Publication.RecoveryOrigin)
	require.NoError(t, republication.Publication.ValidateRefreshPublication())

	// The operator reviewed the carried head before the leader opened, so the request names revision 1.
	receipt, err := leader.PromoteBaseline(ctx, PromotionRequest{OperationID: "promote-after-move", ExpectedRevision: carried.Head.Revision, Actor: "operator"})
	require.NoError(t, err)
	require.Equal(t, PromotionApplied, receipt.Status, receipt.Refusal)
	require.Equal(t, PromotionIdentity{GenerationID: retained.GenerationID, Checksum: retained.PayloadChecksum, Revision: carried.Head.Revision}, receipt.Previous)
	require.Equal(t, report.Packaged.GenerationID, receipt.Promoted.GenerationID)
	require.Equal(t, report.Packaged.Checksum, receipt.Promoted.Checksum)
	require.Greater(t, receipt.Promoted.Revision, receipt.Previous.Revision)
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, receipt.Promoted.Revision, head.Revision)
	acceptance, _, err := leader.fleet.readAcceptance(ctx)
	require.NoError(t, err)
	require.Equal(t, head, acceptance.Head)
	promoted, err := leader.fleet.Publication(ctx, head)
	require.NoError(t, err)
	require.Nil(t, promoted.Publication.RecoveryOrigin, "the promoted head is an ordinary leader publication")
	require.NoError(t, promoted.Publication.ValidateRefreshPublication())
	// The carried publication stays readable as history.
	history, err := leader.fleet.Publication(ctx, carried.Head)
	require.NoError(t, err)
	require.Equal(t, carried.Publication.RecoveryOrigin, history.Publication.RecoveryOrigin)

	replica := fleet.open(t)
	replicaReport, err := replica.BaselineReport()
	require.NoError(t, err)
	require.False(t, replicaReport.Promotable)
	require.Equal(t, replicaReport.Packaged, replicaReport.Retained)
	require.Equal(t, receipt.Promoted.Revision, replicaReport.HeadRevision)
}
