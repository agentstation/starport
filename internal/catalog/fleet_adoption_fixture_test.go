package catalog

import (
	"encoding/json/v2"
	"os"
	"testing"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/stretchr/testify/require"
)

// seedFleetAdoptionCatalog publishes a small, valid Starmap recovery fixture.
// The protocol tests use real leases and native stores. The restored-boundary
// test separately covers the full embedded catalog and runtime acquisition setup.
func seedFleetAdoptionCatalog(t *testing.T, fleet *FleetStore) runtime.FleetSnapshot {
	t.Helper()
	ctx := t.Context()
	data, err := os.ReadFile("testdata/fleet-adoption/publication.json")
	require.NoError(t, err)
	var publication runtime.FleetPublication
	require.NoError(t, json.Unmarshal(data, &publication))
	grant, err := fleet.AcquireLease(ctx, "adoption-fixture", time.Minute)
	require.NoError(t, err)
	publication.Grant = grant
	publication.Expected = runtime.FleetHead{}
	head, err := fleet.CommitPublication(ctx, publication)
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(ctx, head, runtime.FleetHead{}))
	require.NoError(t, fleet.Release(ctx, grant))
	snapshot, err := fleet.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, publication, snapshot.Publication)
	require.NoError(t, runtime.ValidateFleetRecovery(ctx, snapshot))
	require.NoError(t, runtime.ValidateFleetReplay(ctx, snapshot))
	t.Logf("adoption fixture: generation payload %d bytes; retained inputs %d bytes", len(publication.Generation.Payload), len(publication.Recovery.Data))
	return snapshot
}
