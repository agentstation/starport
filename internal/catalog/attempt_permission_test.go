package catalog

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	starmapruntime "github.com/agentstation/starmap/runtime"
	"github.com/stretchr/testify/require"
)

type attemptPermissionReader interface{ AllowsNewAttempt() bool }

type permissionTestSource struct {
	mu         sync.Mutex
	generation catalogs.Generation
	receipt    catalogs.CatalogPermissionEnvelope
	failure    error
}

func (*permissionTestSource) Identity() string { return "internal-authority" }
func (s *permissionTestSource) Read(context.Context) (starmapruntime.SourceRead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return starmapruntime.SourceRead{Changed: true, Generation: s.generation.Copy(), Health: starmapruntime.HealthOK}, s.failure
}
func (s *permissionTestSource) ReadPermission(context.Context) (catalogs.CatalogPermissionEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.receipt, nil
}

type permissionTestCatalogSource struct {
	Source
	*starmapruntime.Runtime
}

func TestSnapshotPermissionTracksWithdrawalWithoutReplacingMetadata(t *testing.T) {
	generation := authoritySnapshotGeneration(t)
	now := generation.Manifest.GeneratedAt.Add(time.Second)
	source := &permissionTestSource{generation: generation, receipt: catalogs.CatalogPermissionEnvelope{
		Version: catalogs.CatalogPermissionEnvelopeVersion, Head: generation.Manifest.AuthorityHead,
		IssuedAt: generation.Manifest.GeneratedAt, ValidUntil: generation.Manifest.GeneratedAt.Add(5 * time.Minute),
	}}
	connected, err := starmapruntime.Open(t.Context(),
		starmapruntime.WithCatalogSource("starmap"), starmapruntime.WithSourceURL("https://authority.example"),
		starmapruntime.WithSourceStartupPolicy("require_authority"), starmapruntime.WithSourceAuthority("enterprise", "production"),
		starmapruntime.WithSource(source), starmapruntime.WithSourcePollInterval(0),
		starmapruntime.WithClock(func() time.Time { return now }),
		starmapruntime.WithPermissionClockUncertainty(func() (time.Duration, bool) { return time.Second, true }),
		starmapruntime.WithClientOptions(starmap.WithCatalogStore(catalogstorage.NewMemory())),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close()) })
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(permissionTestCatalogSource{Source: client, Runtime: connected})
	require.NoError(t, err)
	cold, ok := any(plane.Current()).(attemptPermissionReader)
	require.True(t, ok, "snapshot cannot check its current permission")
	require.False(t, cold.AllowsNewAttempt())
	require.NotNil(t, plane.Current().Catalog())
	_, err = connected.RefreshSource(t.Context())
	require.NoError(t, err)
	require.NoError(t, plane.Activate(connected.State()))
	retained := plane.Current()
	reader := any(retained).(attemptPermissionReader)
	require.True(t, reader.AllowsNewAttempt())
	require.False(t, cold.AllowsNewAttempt(), "embedded metadata has no authority head")
	require.NoError(t, plane.ReplaceAdapters(nil))
	require.True(t, any(plane.Current()).(attemptPermissionReader).AllowsNewAttempt())
	require.Zero(t, testing.AllocsPerRun(100, func() {
		if !reader.AllowsNewAttempt() {
			t.Fatal("valid permission refused")
		}
	}))
	source.mu.Lock()
	source.receipt.Head.Sequence++
	source.receipt.Head.GenerationID = "withdrawn-next"
	source.receipt.Head.RequiredPermissionRevision = "sha256:" + strings.Repeat("c", 64)
	source.failure = io.ErrUnexpectedEOF
	source.mu.Unlock()
	_, err = connected.RefreshSource(t.Context())
	require.Error(t, err)
	require.False(t, reader.AllowsNewAttempt())
	require.False(t, any(plane.Current()).(attemptPermissionReader).AllowsNewAttempt())
	require.Equal(t, generation.Manifest.GenerationID, retained.GenerationID())
	require.NotNil(t, retained.Catalog())
}

func TestRuntimeSnapshotPermissionStopsAfterClose(t *testing.T) {
	r, err := openRuntime(t.Context(), authoritySnapshotBadger(t, t.TempDir()), Settings{
		Source: "embedded", SourceStartupPolicy: "prefer_local", SourceMaxHops: 8,
		TransferIdleTimeout: time.Minute, TransferMaxDuration: time.Minute,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close(context.Background())) })
	reader, ok := any(r.ControlPlane().Current()).(attemptPermissionReader)
	require.True(t, ok, "snapshot cannot check runtime lifetime")
	require.True(t, reader.AllowsNewAttempt())
	require.NoError(t, r.Close(t.Context()))
	require.False(t, reader.AllowsNewAttempt())
}

func TestAuthoritySnapshotNeedsPermissionOwner(t *testing.T) {
	state := runtimeTestState(t, authoritySnapshotGeneration(t))
	state.AuthorityHead = authoritySnapshotGeneration(t).Manifest.AuthorityHead
	snapshot := newRoutableSnapshot(state, 0, nil, nil)
	reader, ok := any(snapshot).(attemptPermissionReader)
	require.True(t, ok)
	require.False(t, reader.AllowsNewAttempt())
	state.AuthorityHead = catalogs.CatalogAuthorityHead{}
	require.True(t, any(newRoutableSnapshot(state, 0, nil, nil)).(attemptPermissionReader).AllowsNewAttempt())
	require.False(t, any((*RoutableSnapshot)(nil)).(attemptPermissionReader).AllowsNewAttempt())
}
