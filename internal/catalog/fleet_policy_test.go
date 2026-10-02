package catalog

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestLeaseRefusesAppliedPolicyMismatch(t *testing.T) {
	fleet, kv, witness, db := fleetTestStoreWithSQL(t)
	ctx := t.Context()
	deployment := fleet.identity.DeploymentID
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, kv.Delete(cleanup, fleet.appliedPolicyKey()))
	})
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = deployment
	publisher, err := openRuntime(ctx, kv, settings, runtimeCollectors{fleet: fleet})
	require.NoError(t, err)
	published, err := publisher.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.NoError(t, publisher.Accept(ctx, published))
	require.NoError(t, publisher.Close(ctx))

	applied := AppliedPolicy{Sequence: 2, Checksum: strings.Repeat("a", 64), OperationID: "apply-2"}
	require.NoError(t, ApplyPolicy(ctx, kv, db, deployment, applied))
	stored, _, err := fleet.readAppliedPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, applied, stored)

	// A replica that still applies an older revision cannot lead.
	stale, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, deployment)
	require.NoError(t, err)
	stale.fencePolicy(strings.Repeat("b", 64))
	_, err = stale.AcquireLease(ctx, "stale-replica", time.Minute)
	var mismatch *PolicyMismatchError
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, applied, mismatch.Applied)
	require.ErrorIs(t, err, starmaperrors.ErrConflict, "the Starmap runtime treats the mismatch as a lease refusal")
	require.ErrorContains(t, err, PolicyMismatch)
	require.Equal(t, PolicyMismatch, stale.PolicyState())

	// The refused replica opens as a consumer and keeps serving the accepted catalog.
	nextSettings := settings
	nextSettings.StateDirectory = filepath.Join(t.TempDir(), "state")
	serving, err := openRuntime(ctx, kv, nextSettings, runtimeCollectors{fleet: stale})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serving.Close(context.Background())) })
	require.Equal(t, PolicyMismatch, serving.PolicyState())
	require.True(t, serving.Status().Usable)
	current, err := serving.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.Equal(t, published.State.GenerationID, current.State.GenerationID)
	require.NoError(t, serving.Accept(ctx, current))
	accepted, err := serving.AcceptedStore().Current(ctx)
	require.NoError(t, err)
	require.Equal(t, published.State.GenerationID, accepted.Manifest.GenerationID)
	_, err = serving.Refresh(ctx)
	require.ErrorIs(t, err, starmaperrors.ErrConflict, "a refused replica sends no source request")
	require.Equal(t, PolicyMismatch, serving.PolicyState())

	// A replica on the applied revision leads.
	matching, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, deployment)
	require.NoError(t, err)
	matching.fencePolicy(applied.Checksum)
	lease, err := matching.AcquireLease(ctx, "matching-replica", time.Minute)
	require.NoError(t, err)
	require.Equal(t, PolicyMatch, matching.PolicyState())
	require.NoError(t, matching.Release(ctx, lease))

	// A resumed apply is idempotent. An older or a different operation refuses.
	require.NoError(t, ApplyPolicy(ctx, kv, db, deployment, applied))
	older := AppliedPolicy{Sequence: 1, Checksum: strings.Repeat("c", 64), OperationID: "apply-1"}
	require.ErrorIs(t, ApplyPolicy(ctx, kv, db, deployment, older), starmaperrors.ErrConflict)
	other := AppliedPolicy{Sequence: 2, Checksum: strings.Repeat("c", 64), OperationID: "apply-other"}
	require.ErrorIs(t, ApplyPolicy(ctx, kv, db, deployment, other), starmaperrors.ErrConflict)
	stored, _, err = fleet.readAppliedPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, applied, stored)
}

// TestRenewRefusesAppliedPolicyMismatch proves that a sitting leader cannot
// block an apply. The apply writes the record while the leader holds the
// lease, and the next renewal of the leader refuses.
func TestRenewRefusesAppliedPolicyMismatch(t *testing.T) {
	fleet, kv, witness, db := fleetTestStoreWithSQL(t)
	ctx := t.Context()
	deployment := fleet.identity.DeploymentID
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, kv.Delete(cleanup, fleet.appliedPolicyKey()))
	})
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = deployment
	publisher, err := openRuntime(ctx, kv, settings, runtimeCollectors{fleet: fleet})
	require.NoError(t, err)
	published, err := publisher.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.NoError(t, publisher.Accept(ctx, published))
	require.NoError(t, publisher.Close(ctx))

	first := AppliedPolicy{Sequence: 1, Checksum: strings.Repeat("a", 64), OperationID: "apply-1"}
	require.NoError(t, ApplyPolicy(ctx, kv, db, deployment, first))
	leader, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, deployment)
	require.NoError(t, err)
	leader.fencePolicy(first.Checksum)
	lease, err := leader.AcquireLease(ctx, "sitting-leader", time.Minute)
	require.NoError(t, err)
	lease, err = leader.Renew(ctx, lease, time.Minute)
	require.NoError(t, err)
	require.Equal(t, PolicyMatch, leader.PolicyState())

	// The apply never waits for the held lease and leaves the grant in place.
	second := AppliedPolicy{Sequence: 2, Checksum: strings.Repeat("b", 64), OperationID: "apply-2"}
	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	require.NoError(t, ApplyPolicy(applyCtx, kv, db, deployment, second))
	stored, _, err := fleet.readAppliedPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, second, stored)
	grant, err := encodeFleetGrant(lease)
	require.NoError(t, err)
	held, _, err := fleet.store.ReadWithLifetime(ctx, fleet.prefix+"lease", 4096)
	require.NoError(t, err)
	require.Equal(t, grant, held)

	// The next renewal of the sitting leader refuses.
	_, err = leader.Renew(ctx, lease, time.Minute)
	var mismatch *PolicyMismatchError
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, second, mismatch.Applied)
	require.Equal(t, first.Checksum, mismatch.Local)
	require.ErrorIs(t, err, starmaperrors.ErrConflict, "the Starmap keeper treats the refusal as a lost lease")
	require.Equal(t, PolicyMismatch, leader.PolicyState())
	require.NoError(t, leader.Release(ctx, lease))

	// The former leader opens as a consumer and keeps serving the accepted catalog.
	nextSettings := settings
	nextSettings.StateDirectory = filepath.Join(t.TempDir(), "state")
	serving, err := openRuntime(ctx, kv, nextSettings, runtimeCollectors{fleet: leader})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serving.Close(context.Background())) })
	require.Equal(t, PolicyMismatch, serving.PolicyState())
	require.True(t, serving.Status().Usable)
	current, err := serving.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.Equal(t, published.State.GenerationID, current.State.GenerationID)
	require.NoError(t, serving.Accept(ctx, current))

	// A replica on the applied revision leads and renews.
	matching, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, deployment)
	require.NoError(t, err)
	matching.fencePolicy(second.Checksum)
	next, err := matching.AcquireLease(ctx, "matching-replica", time.Minute)
	require.NoError(t, err)
	next, err = matching.Renew(ctx, next, time.Minute)
	require.NoError(t, err)
	require.Equal(t, PolicyMatch, matching.PolicyState())
	require.NoError(t, matching.Release(ctx, next))
}

func TestApplyPolicyRequiresSharedFleet(t *testing.T) {
	applied := AppliedPolicy{Sequence: 1, Checksum: strings.Repeat("a", 64), OperationID: "apply-1"}
	require.ErrorIs(t, ApplyPolicy(t.Context(), storage.NewMockStore(), nil, "local", applied), ErrPolicyFenceUnavailable)
	require.Error(t, ApplyPolicy(t.Context(), storage.NewMockStore(), nil, "local", AppliedPolicy{Sequence: 1}))
	var unfenced *FleetStore
	unfenced.fencePolicy("ignored")
	require.Empty(t, unfenced.PolicyState())
}
