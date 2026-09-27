package catalog

import (
	"fmt"
	"testing"
	"time"

	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestFleetAcceptanceRetainsDistinctRollbackGenerations(t *testing.T) {
	fleet, _, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	first, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, runtime.FleetHead{}, "rollback"))
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(ctx, first, runtime.FleetHead{}))
	publication := fleetTestPublication(t, grant, first, "current")
	head, err := fleet.CommitPublication(ctx, publication)
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(ctx, head, first))
	for i := range catalogGenerationIndexCap + 1 {
		previous := head
		publication.Expected = previous
		publication.Recovery.Data = []byte(fmt.Sprintf(`{"revision":%d}`, i))
		publication.Recovery.Checksum = payloadDigest(publication.Recovery.Data)
		head, err = fleet.CommitPublication(ctx, publication)
		require.NoError(t, err)
		require.NoError(t, fleet.AcceptPublication(ctx, head, previous))
	}
	history, err := fleet.AcceptedHistory(ctx)
	require.NoError(t, err)
	require.Len(t, history, 2, "input-only revisions must not consume distinct rollback slots")
	require.Equal(t, first.GenerationID, history[0].GenerationID)
	report, err := fleet.Collect(ctx, catalogstorage.RetentionRequest{ExpectedGenerationID: head.GenerationID, MaxGenerations: 1, MaxBytes: 1 << 30})
	require.NoError(t, err)
	require.Equal(t, 2, report.After.Generations, "the smaller requested limit cannot remove protected rollback content")
	_, err = fleet.Get(ctx, first.GenerationID)
	require.NoError(t, err, "collection must preserve the distinct accepted rollback generation")
}

func TestFleetStoreCompleteKeyLossRequiresRecovery(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	head, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, runtime.FleetHead{}, "established"))
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(ctx, head, runtime.FleetHead{}))
	keys, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
	require.NoError(t, err)
	require.NoError(t, kv.BatchDelete(ctx, keys))
	restarted, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, fleet.identity.DeploymentID)
	if err != nil {
		return
	}
	_, err = restarted.CurrentHead(ctx)
	require.Error(t, err)
	require.NotErrorIs(t, err, starmaperrors.ErrNotFound, "complete loss must not look like an unused fleet")
}
