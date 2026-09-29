package catalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agentstation/starmap/acquisition"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestFleetAdoptionAcceptsRestoredClosedBoundary(t *testing.T) {
	fleet, kv, witness, db := fleetTestStoreWithSQL(t)
	ctx := t.Context()
	provider := kv.(storage.IncarnationProvider)
	t.Cleanup(func() { require.NoError(t, kv.Delete(context.Background(), "recovery:authority:v1")) })
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = fleet.identity.DeploymentID
	settings.Values = map[string]string{catalogconfig.AcquisitionSources: ""}
	providers, err := acquisition.NewAcquirer()
	require.NoError(t, err)
	metadata, err := settings.metadataCollector()
	require.NoError(t, err)
	connected, err := openRuntime(ctx, kv, settings, runtimeCollectors{fleet: fleet, providers: providers, metadata: metadata})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
	_, err = connected.runtime.RefreshSource(ctx)
	require.NoError(t, err)
	candidate, err := connected.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.NoError(t, connected.Accept(ctx, candidate))
	require.NoError(t, connected.Close(ctx))
	original, err := fleet.AcceptedPublication(ctx)
	require.NoError(t, err)
	closed, err := witness.Close(ctx, fleet.approval)
	require.NoError(t, err)
	// Model the closed boundary produced by PrepareSQLRestore. This fixture
	// qualifies catalog adoption only, not complete history reconciliation.
	_, err = db.ExecContext(ctx, db.Bind("UPDATE catalog_recovery SET backend_id='',evidence=? WHERE deployment_id=? AND epoch=? AND gate_open=0"), "restore-prepared:fixture", closed.DeploymentID, closed.Epoch)
	require.NoError(t, err)
	closed, err = witness.Current(ctx, closed.DeploymentID)
	require.NoError(t, err)
	require.Empty(t, closed.BackendID)
	request := FleetAdoptionRequest{SourceApproval: fleet.approval, Closed: closed, Head: original.Head,
		BackendID: fleet.identity.BackendID, OperationID: "restore-empty-backend", Evidence: "fixture-fenced-and-reconciled"}
	options, err := settings.starmapOptions()
	require.NoError(t, err)
	options = append(options, runtime.WithAcquirer(providers), runtime.WithSourceAcquirer(metadata))

	changed := request
	changed.Closed.BackendID = fleet.identity.BackendID
	_, err = adoptFleet(ctx, provider, witness, changed, options)
	require.ErrorIs(t, err, recovery.ErrConflict, "adoption must compare the exact restored SQL boundary")
	_, err = witness.Approved(ctx, closed.DeploymentID)
	require.ErrorIs(t, err, recovery.ErrClosed)

	fault := &lostAdoptionReply{IncarnationProvider: provider}
	require.ErrorContains(t, prepareFleetAdoption(ctx, fault, witness, request, options), "lost catalog adoption response")
	require.True(t, fault.injected)
	current, err := witness.Current(ctx, closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, closed, current, "lost preparation acknowledgement must not open authority")
	require.NoError(t, PrepareFleetAdoption(ctx, provider, witness, request, settings))
	require.NoError(t, PrepareFleetAdoption(ctx, provider, witness, request, settings), "an exact preparation retry must preserve the closed gate")
	current, err = witness.Current(ctx, closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, closed, current)
	_, err = witness.OpenAuthority(ctx, provider, closed.DeploymentID)
	require.ErrorIs(t, err, recovery.ErrClosed)
	_, err = NewFleetStore(ctx, provider, witness, closed.DeploymentID)
	require.ErrorIs(t, err, recovery.ErrClosed)
	conflict := request
	conflict.OperationID = "competing-restoration"
	require.Error(t, prepareFleetAdoption(ctx, provider, witness, conflict, options))

	approved, err := adoptFleet(ctx, provider, witness, request, options)
	require.NoError(t, err)
	require.Equal(t, request.approval(), approved)
	repeated, err := adoptFleet(ctx, provider, witness, request, options)
	require.NoError(t, err)
	require.Equal(t, approved, repeated)
	require.ErrorIs(t, prepareFleetAdoption(ctx, provider, witness, request, options), recovery.ErrConflict, "preparation cannot claim an open deployment remains restricted")
	restored, err := NewFleetStore(ctx, provider, witness, closed.DeploymentID)
	require.NoError(t, err)
	publication, err := restored.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, original.Publication, publication.Publication)
	require.Equal(t, original.Head, publication.Adoption.Previous)
	_, err = witness.OpenAuthority(ctx, provider, closed.DeploymentID)
	require.NoError(t, err)
	_, err = fleet.CurrentHead(ctx)
	require.Error(t, err, "the source approval must remain obsolete")
}
