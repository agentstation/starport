package catalog

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
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
	b := catalogs.NewEmpty()
	require.NoError(t, b.SetAuthor(catalogs.Author{ID: "enterprise-fixture", Name: "Enterprise fixture"}))
	require.NoError(t, b.SetAuthorModel("enterprise-fixture", catalogs.Model{ID: model, Name: model, Authors: []catalogs.Author{{ID: "enterprise-fixture", Name: "Enterprise fixture"}}}))
	c, err := b.Build()
	require.NoError(t, err)
	at := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	input := runtimeTestGeneration(t, "authority-acceptance", c, at)
	g, err := permission.PrepareGeneration(input, permission.GenerationConfig{AuthorityID: "enterprise", PolicyID: "production", Sequence: sequence})
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
	require.NoError(t, os.MkdirAll(directory, 0700))
	require.NoError(t, os.Chmod(directory, 0700))
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
		starmapruntime.WithClock(func() time.Time { return time.Date(2026, 9, 11, 0, 0, 1, 0, time.UTC) }),
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
	orphan := newRoutableSnapshot(state, 0, nil, nil)
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
