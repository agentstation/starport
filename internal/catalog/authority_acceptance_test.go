package catalog

import (
	"context"
	"io/fs"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/catalogs/permission"
	starmapruntime "github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// authorityAcceptanceGeneration permits one authored model and no embedded membership.
func authorityAcceptanceGeneration(t *testing.T, sequence uint64, model string) catalogs.Generation {
	t.Helper()
	return authorityAcceptanceGenerationFor(t, sequence, model, "enterprise")
}

func authorityAcceptanceGenerationFor(t *testing.T, sequence uint64, model, authority string) catalogs.Generation {
	t.Helper()
	b := catalogs.NewEmpty()
	require.NoError(t, b.SetAuthor(catalogs.Author{ID: "enterprise-fixture", Name: "Enterprise fixture"}))
	require.NoError(t, b.SetAuthorModel("enterprise-fixture", catalogs.Model{ID: model, Name: model, Authors: []catalogs.Author{{ID: "enterprise-fixture", Name: "Enterprise fixture"}}}))
	c, err := b.Build()
	require.NoError(t, err)
	at := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	input := runtimeTestGeneration(t, "authority-acceptance", c, at)
	g, err := permission.PrepareGeneration(input, permission.GenerationConfig{AuthorityID: authority, PolicyID: "production", Sequence: sequence})
	require.NoError(t, err)
	return g
}

func authorityAcceptanceSource(t *testing.T) *permissionTestSource {
	t.Helper()
	g := authorityAcceptanceGeneration(t, 1, "allowed")
	return &permissionTestSource{generation: g, receipt: catalogs.CatalogPermissionEnvelope{
		Version: catalogs.CatalogPermissionEnvelopeVersion, Head: g.Manifest.AuthorityHead,
		IssuedAt: g.Manifest.GeneratedAt, ValidUntil: g.Manifest.GeneratedAt.Add(5 * time.Minute),
	}}
}

// openAuthorityAcceptance exercises Starport's durable candidate and accepted stores.
// The test supplies a deterministic qualified clock. Native clock qualification is separate.
func openAuthorityAcceptance(t *testing.T, kv storage.KVStore, directory string, source *permissionTestSource, authority, policy string) *Runtime {
	t.Helper()
	return openAuthorityAcceptanceWithClock(t, kv, directory, source, authority, policy, func() time.Time { return time.Date(2026, 9, 11, 0, 0, 1, 0, time.UTC) })
}

func openAuthorityAcceptanceWithClock(t *testing.T, kv storage.KVStore, directory string, source *permissionTestSource, authority, policy string, clock func() time.Time) *Runtime {
	t.Helper()
	accepted, err := NewGenerationStore(kv)
	require.NoError(t, err)
	candidates, err := newCandidateGenerationStore(kv)
	require.NoError(t, err)
	leases, err := NewLeaseStore(kv)
	require.NoError(t, err)
	client, err := starmap.NewContext(t.Context(), starmap.WithCatalogStore(accepted))
	require.NoError(t, err)
	settings := Settings{Source: "starmap", SourceURL: "https://authority.example", SourceStartupPolicy: "require_authority", SourceAuthorityID: authority, SourcePolicyID: policy,
		StateDirectory: directory, SourceMaxHops: 8, TransferIdleTimeout: time.Minute, TransferMaxDuration: time.Minute}
	options, err := settings.starmapOptions()
	require.NoError(t, err)
	options = append(options, starmapruntime.WithSource(source), starmapruntime.WithLeaseStore(leases),
		starmapruntime.WithClientOptions(starmap.WithCatalogStore(candidates)),
		starmapruntime.WithClock(clock),
		starmapruntime.WithPermissionClockUncertainty(func() (time.Duration, bool) { return time.Millisecond, true }))
	connected, err := starmapruntime.Open(t.Context(), options...)
	require.NoError(t, err)
	plane, err := Open(acceptedCatalogSource{Source: client, catalogAttemptPermission: connected})
	require.NoError(t, err)
	r := newRuntime(connected, nil, candidates, accepted, plane, leases)
	t.Cleanup(func() { require.NoError(t, r.Close(context.Background())) })
	return r
}

func acceptAuthorityGeneration(t *testing.T, r *Runtime) {
	t.Helper()
	candidate, err := r.RefreshCandidate(t.Context(), 0)
	require.NoError(t, err)
	require.NoError(t, r.Accept(t.Context(), candidate))
	require.NoError(t, r.ControlPlane().Activate(candidate.State))
	require.True(t, r.ControlPlane().Current().AllowsNewAttempt())
}

func TestAuthorityCatalogExcludesEmbeddedMembership(t *testing.T) {
	source := authorityAcceptanceSource(t)
	r := openAuthorityAcceptance(t, authoritySnapshotBadger(t, t.TempDir()), filepath.Join(t.TempDir(), "runtime"), source, "enterprise", "production")
	acceptAuthorityGeneration(t, r)
	current := r.ControlPlane().Current()
	require.Len(t, current.Catalog().Definitions(), 1)
	require.Equal(t, catalogs.ModelDefinitionID("enterprise-fixture/allowed"), current.Catalog().Definitions()[0].ID)
	embedded, err := starmap.New()
	require.NoError(t, err)
	require.Greater(t, len(embedded.Catalog().Definitions()), 1)
	baseline := runtimeTestGeneration(t, "baseline-union-attempt", embedded.Catalog(), source.generation.Manifest.GeneratedAt.Add(time.Minute))
	require.NoError(t, r.candidates.Commit(t.Context(), baseline, source.generation.Manifest.GenerationID))
	require.Error(t, r.Accept(t.Context(), Candidate{State: runtimeTestState(t, baseline)}))
	accepted, err := r.accepted.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, source.generation.Manifest.AuthorityHead, accepted.Manifest.AuthorityHead)
	require.Same(t, current, r.ControlPlane().Current())
	require.Len(t, current.Catalog().Definitions(), 1)
	require.True(t, current.AllowsNewAttempt())
}

func TestAuthorityColdStartupRefusesWithoutAcceptedOwner(t *testing.T) {
	source := authorityAcceptanceSource(t)
	source.failure, source.permissionFailure = fs.ErrNotExist, fs.ErrNotExist
	r := openAuthorityAcceptance(t, authoritySnapshotBadger(t, t.TempDir()), filepath.Join(t.TempDir(), "runtime"), source, "enterprise", "production")
	current := r.ControlPlane().Current()
	require.NotNil(t, current.Catalog())
	require.False(t, current.AllowsNewAttempt())
	require.NotNil(t, current.CheckNewAttempt())
	require.Error(t, r.runtime.RefreshPermission(t.Context()))
	require.False(t, current.AllowsNewAttempt())
	// Metadata without its permission owner cannot authorize an authority snapshot either.
	g := source.generation
	state := runtimeTestState(t, g)
	state.AuthorityHead = g.Manifest.AuthorityHead
	orphan := mustRoutableSnapshot(t, state, 0, nil, nil)
	require.False(t, orphan.AllowsNewAttempt())
}

func TestAuthorityWarmStartupUsesOnlyRetainedMatchingOwner(t *testing.T) {
	kvPath, directory := t.TempDir(), filepath.Join(t.TempDir(), "runtime")
	kv := authoritySnapshotBadger(t, kvPath)
	source := authorityAcceptanceSource(t)
	r := openAuthorityAcceptance(t, kv, directory, source, "enterprise", "production")
	acceptAuthorityGeneration(t, r)
	accepted := r.ControlPlane().Current()
	require.NoError(t, r.Close(t.Context()))
	require.NoError(t, kv.Close())
	source.mu.Lock()
	source.failure, source.permissionFailure = fs.ErrNotExist, fs.ErrNotExist
	source.mu.Unlock()
	for _, owner := range []struct {
		name, authority, policy string
		allowed                 bool
	}{
		{"matching", "enterprise", "production", true},
		{"different authority", "other", "production", false},
		{"different policy", "enterprise", "other", false},
	} {
		t.Run(owner.name, func(t *testing.T) {
			reopened := authoritySnapshotBadger(t, kvPath)
			warm := openAuthorityAcceptance(t, reopened, directory, source, owner.authority, owner.policy)
			current := warm.ControlPlane().Current()
			require.NotNil(t, current.Catalog())
			require.Equal(t, accepted.GenerationID(), current.GenerationID())
			require.Equal(t, owner.allowed, current.AllowsNewAttempt())
			require.NoError(t, warm.Close(t.Context()))
			require.NoError(t, reopened.Close())
		})
	}
}

func TestAuthorityFutureSchemaPermissionEnvelope(t *testing.T) {
	for _, test := range []struct {
		name, model string
		allowed     bool
	}{
		{"unchanged permission", "allowed", true},
		{"withdrawn permission", "replacement", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := authorityAcceptanceSource(t)
			r := openAuthorityAcceptance(t, authoritySnapshotBadger(t, t.TempDir()), filepath.Join(t.TempDir(), "runtime"), source, "enterprise", "production")
			acceptAuthorityGeneration(t, r)
			retained := r.ControlPlane().Current()
			next := authorityAcceptanceGeneration(t, 2, test.model)
			require.Equal(t, test.allowed, next.Manifest.AuthorityHead.RequiredPermissionRevision == source.generation.Manifest.AuthorityHead.RequiredPermissionRevision)
			next.Manifest.SchemaVersion++
			next.Manifest.ConsumerCompatibility.MinSchemaVersion = next.Manifest.SchemaVersion
			next.Manifest.ConsumerCompatibility.MaxSchemaVersion = next.Manifest.SchemaVersion
			source.mu.Lock()
			source.generation = next
			source.receipt.Head = next.Manifest.AuthorityHead
			source.mu.Unlock()
			_, err := r.RefreshCandidate(t.Context(), 0)
			require.Error(t, err)
			require.Equal(t, test.allowed, retained.AllowsNewAttempt())
			if test.allowed {
				require.Nil(t, retained.CheckNewAttempt())
			} else {
				require.NotNil(t, retained.CheckNewAttempt())
			}
			require.Same(t, retained, r.ControlPlane().Current())
			accepted, err := r.accepted.Current(t.Context())
			require.NoError(t, err)
			require.Equal(t, retained.GenerationID(), accepted.Manifest.GenerationID)
			require.NotNil(t, retained.Catalog())
		})
	}
}

func TestAuthorityPartitionRefusesAtPermissionExpiry(t *testing.T) {
	source := authorityAcceptanceSource(t)
	var now atomic.Int64
	now.Store(source.receipt.IssuedAt.Add(time.Second).UnixNano())
	r := openAuthorityAcceptanceWithClock(t, authoritySnapshotBadger(t, t.TempDir()), filepath.Join(t.TempDir(), "runtime"), source, "enterprise", "production", func() time.Time { return time.Unix(0, now.Load()) })
	acceptAuthorityGeneration(t, r)
	retained := r.ControlPlane().Current()
	source.mu.Lock()
	source.failure, source.permissionFailure = fs.ErrNotExist, fs.ErrNotExist
	source.mu.Unlock()
	require.Error(t, r.runtime.RefreshPermission(t.Context()))
	require.True(t, retained.AllowsNewAttempt())
	now.Store(source.receipt.ValidUntil.Add(-2 * time.Millisecond).UnixNano())
	require.True(t, retained.AllowsNewAttempt())
	now.Store(source.receipt.ValidUntil.UnixNano())
	require.False(t, retained.AllowsNewAttempt())
	require.NotNil(t, retained.CheckNewAttempt())
	require.Same(t, retained, r.ControlPlane().Current())
	require.Len(t, retained.Catalog().Definitions(), 1)
}

func TestAuthorityWithdrawalPersistsAcrossOfflineRestart(t *testing.T) {
	kvPath, directory := t.TempDir(), filepath.Join(t.TempDir(), "runtime")
	kv := authoritySnapshotBadger(t, kvPath)
	source := authorityAcceptanceSource(t)
	r := openAuthorityAcceptance(t, kv, directory, source, "enterprise", "production")
	acceptAuthorityGeneration(t, r)
	retained := r.ControlPlane().Current()
	next := authorityAcceptanceGeneration(t, 2, "replacement")
	source.mu.Lock()
	source.receipt.Head = next.Manifest.AuthorityHead
	source.failure = fs.ErrNotExist
	source.mu.Unlock()
	_, err := r.RefreshCandidate(t.Context(), 0)
	require.Error(t, err)
	require.False(t, retained.AllowsNewAttempt())
	require.NoError(t, r.Close(t.Context()))
	require.NoError(t, kv.Close())
	source.mu.Lock()
	source.permissionFailure = fs.ErrNotExist
	source.mu.Unlock()
	reopened := authoritySnapshotBadger(t, kvPath)
	warm := openAuthorityAcceptance(t, reopened, directory, source, "enterprise", "production")
	current := warm.ControlPlane().Current()
	require.Equal(t, retained.GenerationID(), current.GenerationID())
	require.Len(t, current.Catalog().Definitions(), 1)
	require.False(t, current.AllowsNewAttempt())
	require.NotNil(t, current.CheckNewAttempt())
}

func TestAuthorityColdFollowerCannotServeBaseline(t *testing.T) {
	kv := authoritySnapshotBadger(t, t.TempDir())
	leases, err := NewLeaseStore(kv)
	require.NoError(t, err)
	held, err := leases.AcquireLease(t.Context(), "other-owner", time.Hour)
	require.NoError(t, err)
	source := authorityAcceptanceSource(t)
	follower := openAuthorityAcceptance(t, kv, filepath.Join(t.TempDir(), "runtime"), source, "enterprise", "production")
	require.Equal(t, "lease_lost", follower.Status().Lease)
	current := follower.ControlPlane().Current()
	require.NotNil(t, current.Catalog())
	require.False(t, current.AllowsNewAttempt())
	require.NoError(t, follower.runtime.RefreshPermission(t.Context()))
	require.False(t, current.AllowsNewAttempt(), "a receipt alone cannot authorize baseline membership")
	_, _ = follower.RefreshCandidate(t.Context(), 0)
	require.False(t, current.AllowsNewAttempt())
	require.Equal(t, "lease_lost", follower.Status().Lease)
	epoch, err := leases.CurrentEpoch(t.Context())
	require.NoError(t, err)
	require.Equal(t, held.Epoch, epoch)
	_, err = follower.accepted.Current(t.Context())
	require.Error(t, err)
}

func TestAuthorityChangeRequiresMatchingAcceptance(t *testing.T) {
	for _, mode := range []string{"approved", "expired"} {
		t.Run(mode, func(t *testing.T) {
			kvPath, directory := t.TempDir(), filepath.Join(t.TempDir(), "runtime")
			kv := authoritySnapshotBadger(t, kvPath)
			source := authorityAcceptanceSource(t)
			original := openAuthorityAcceptance(t, kv, directory, source, "enterprise", "production")
			acceptAuthorityGeneration(t, original)
			oldID := original.ControlPlane().Current().GenerationID()
			require.NoError(t, original.Close(t.Context()))
			require.NoError(t, kv.Close())
			next := authorityAcceptanceGenerationFor(t, 1, "replacement", "new-authority")
			source.mu.Lock()
			source.generation = next
			source.receipt.Head = next.Manifest.AuthorityHead
			source.mu.Unlock()
			reopened := authoritySnapshotBadger(t, kvPath)
			var now atomic.Int64
			now.Store(source.receipt.IssuedAt.Add(time.Second).UnixNano())
			changed := openAuthorityAcceptanceWithClock(t, reopened, directory, source, "new-authority", "production", func() time.Time { return time.Unix(0, now.Load()) })
			require.Equal(t, oldID, changed.ControlPlane().Current().GenerationID())
			require.False(t, changed.ControlPlane().Current().AllowsNewAttempt())
			candidate, err := changed.RefreshCandidate(t.Context(), 0)
			require.NoError(t, err)
			unselected := authorityAcceptanceGenerationFor(t, 2, "unselected", "new-authority")
			require.NoError(t, changed.candidates.Commit(t.Context(), unselected, candidate.State.GenerationID))
			foreign := runtimeTestState(t, unselected)
			foreign.AuthorityHead = unselected.Manifest.AuthorityHead
			require.Error(t, changed.Accept(t.Context(), Candidate{State: foreign, Epoch: candidate.Epoch}))
			if mode == "expired" {
				now.Store(source.receipt.ValidUntil.UnixNano())
				require.Error(t, changed.Accept(t.Context(), candidate))
				retained, err := changed.accepted.Current(t.Context())
				require.NoError(t, err)
				require.Equal(t, oldID, retained.Manifest.GenerationID)
				require.False(t, changed.ControlPlane().Current().AllowsNewAttempt())
				return
			}
			require.NoError(t, changed.Accept(t.Context(), candidate))
			require.NoError(t, changed.ControlPlane().Activate(candidate.State))
			require.True(t, changed.ControlPlane().Current().AllowsNewAttempt())
			require.NotEqual(t, oldID, changed.ControlPlane().Current().GenerationID())
			definitions := changed.ControlPlane().Current().Catalog().Definitions()
			require.Len(t, definitions, 1)
			require.Equal(t, catalogs.ModelDefinitionID("enterprise-fixture/replacement"), definitions[0].ID)
		})
	}
}
