package catalog

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/catalog/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestFleetRuntimeRejectsUnapprovedBackend(t *testing.T) {
	kv, witness, db := fleetTestStores(t)
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = "unapproved-" + rand.Text()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, db.Bind("DELETE FROM catalog_recovery WHERE deployment_id = ?"), settings.DeploymentID)
		require.NoError(t, err)
	})
	lookup := func(string) (string, bool) { return "", false }
	_, err := OpenRuntimeWithRecovery(t.Context(), kv, nil, settings, lookup)
	require.ErrorContains(t, err, "PostgreSQL witness")
	_, err = OpenRuntimeWithRecovery(t.Context(), kv, db, settings, lookup)
	require.ErrorIs(t, err, recovery.ErrClosed)
	_, err = witness.Initialize(t.Context(), settings.DeploymentID)
	require.NoError(t, err)
	_, err = OpenRuntimeWithRecovery(t.Context(), kv, db, settings, lookup)
	require.ErrorIs(t, err, recovery.ErrClosed)
	closed, err := witness.Current(t.Context(), settings.DeploymentID)
	require.NoError(t, err)
	provider := kv.(storage.IncarnationProvider)
	identity, err := provider.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	_, err = witness.Approve(t.Context(), closed, identity, "isolated-runtime-test")
	require.NoError(t, err)
	// This isolated fixture explicitly starts unused. Real initialization has its own native tests.
	_, err = db.ExecContext(t.Context(), db.Bind("UPDATE catalog_recovery SET bootstrap_allowed = 1 WHERE deployment_id = ?"), settings.DeploymentID)
	require.NoError(t, err)
	fleet, err := NewFleetStore(t.Context(), provider, witness, settings.DeploymentID)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		keys, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
		require.NoError(t, err)
		require.NoError(t, kv.BatchDelete(ctx, keys))
	})
	grant, err := fleet.AcquireLease(t.Context(), "publisher", time.Minute)
	require.NoError(t, err)
	publication := fleetTestPublication(t, grant, runtime.FleetHead{}, "diagnostic")
	head, err := fleet.CommitPublication(t.Context(), publication)
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(t.Context(), head, runtime.FleetHead{}))
	readOnly, err := storage.OpenReadOnly(storage.Config{Type: storage.StorageTypeValkey, Valkey: storage.ValkeyConfig{DeploymentID: "contract-tests", URL: os.Getenv("TEST_VALKEY_URL")}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, readOnly.Close()) })
	accepted, err := OpenAcceptedStore(t.Context(), readOnly, db, settings.DeploymentID)
	require.NoError(t, err)
	got, err := accepted.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, publication.Generation.Manifest, got.Manifest)
	history, err := accepted.History(t.Context())
	require.NoError(t, err)
	require.Len(t, history, 1)
	_, err = accepted.fleet.AcquireLease(t.Context(), "inspector", time.Minute)
	require.Error(t, err)
	bound, err := readOnly.(storage.IncarnationProvider).BindIncarnation(t.Context(), identity)
	require.NoError(t, err)
	require.ErrorIs(t, bound.CompareAndSwap(t.Context(), []storage.CompareAndSwapMutation{{Key: fleet.prefix + "unexpected", NewValue: []byte("no")}}), storage.ErrReadOnly)
	_, err = kv.Get(t.Context(), fleet.prefix+"unexpected")
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestFleetRuntimePreservesOriginalGrantThroughAcceptance(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	root := t.TempDir()
	settings := identityTestSettings(filepath.Join(root, "state"), "", "127.0.0.1:0")
	settings.DeploymentID = fleet.identity.DeploymentID
	first, err := openRuntime(t.Context(), kv, settings, runtimeCollectors{fleet: fleet})
	require.NoError(t, err)
	closed := false
	t.Cleanup(func() {
		if !closed {
			require.NoError(t, first.Close(context.Background()))
		}
	})
	candidate, err := first.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, runtime.FleetHead{}, candidate.FleetHead)
	original, err := fleet.Publication(t.Context(), candidate.FleetHead)
	require.NoError(t, err)
	require.Equal(t, original.Publication.Grant.Epoch, candidate.Epoch)
	encoded, err := json.Marshal(original)
	require.NoError(t, err)
	t.Logf("retained baseline publication: %d encoded bytes, %d recovery bytes", len(encoded), len(original.Publication.Recovery.Data))
	require.Less(t, int64(len(encoded))*catalogGenerationIndexCap, int64(fleetRetentionMaxBytes), "the protected receipt window must fit the native byte bound before local acquisition")
	require.NoError(t, first.Close(t.Context()))
	closed = true
	require.NoError(t, os.RemoveAll(root))

	followerStore, err := NewFleetStore(t.Context(), kv.(storage.IncarnationProvider), witness, fleet.identity.DeploymentID)
	require.NoError(t, err)
	nextSettings := settings
	nextSettings.StateDirectory = filepath.Join(t.TempDir(), "state")
	second, err := openRuntime(t.Context(), kv, nextSettings, runtimeCollectors{fleet: followerStore})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close(context.Background())) })
	current, err := second.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.Equal(t, candidate.State.PayloadChecksum, current.State.PayloadChecksum)
	require.Greater(t, current.FleetHead.Revision, candidate.FleetHead.Revision)
	require.Greater(t, current.Epoch, candidate.Epoch)
	require.Error(t, second.Accept(t.Context(), candidate), "a stale validation cannot borrow the successor grant")
	require.NoError(t, second.Accept(t.Context(), current))
	accepted, err := second.AcceptedStore().Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, current.State.GenerationID, accepted.Manifest.GenerationID)
	// An accepted record remains idempotent after its acquisition grant ends.
	snapshot, err := followerStore.Publication(t.Context(), current.FleetHead)
	require.NoError(t, err)
	require.NoError(t, followerStore.Release(t.Context(), snapshot.Publication.Grant))
	require.NoError(t, second.Accept(t.Context(), current))
}

// unchangedFleetSource reports no new upstream data after a live owner change.
type unchangedFleetSource struct{}

func (unchangedFleetSource) Identity() string { return "unchanged-fleet-source" }
func (unchangedFleetSource) Read(ctx context.Context) (runtime.SourceRead, error) {
	return runtime.SourceRead{Health: runtime.HealthOK}, ctx.Err()
}

func TestFleetRuntimeLiveTakeoverAcceptance(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	open := func(id string, store *FleetStore) *runtime.Runtime {
		connected, err := runtime.Open(t.Context(), runtime.WithFleetStore(store),
			runtime.WithStateDirectory(filepath.Join(t.TempDir(), "state")),
			runtime.WithSchedulerIdentity(id), runtime.WithSource(unchangedFleetSource{}),
			runtime.WithSourceRefreshMode("manual"))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, connected.Close()) })
		return connected
	}
	leader := open("live-owner", fleet)
	before, ok := leader.FleetStatus()
	require.True(t, ok)
	original, err := fleet.Publication(t.Context(), before.Head)
	require.NoError(t, err)
	followerStore, err := NewFleetStore(t.Context(), kv.(storage.IncarnationProvider), witness, fleet.identity.DeploymentID)
	require.NoError(t, err)
	follower := open("live-follower", followerStore)
	status, _ := follower.FleetStatus()
	require.Equal(t, before.Head, status.Head)
	require.NoError(t, leader.Close())
	_, err = follower.RefreshSource(t.Context())
	require.NoError(t, err)
	after, _ := follower.FleetStatus()
	require.Greater(t, after.Head.Revision, before.Head.Revision)
	require.Equal(t, before.Head.GenerationID, after.Head.GenerationID)
	require.Error(t, followerStore.AcceptPublication(t.Context(), before.Head, runtime.FleetHead{}))
	require.NoError(t, followerStore.AcceptPublication(t.Context(), after.Head, runtime.FleetHead{}))
	retried, err := fleet.CommitPublication(t.Context(), original.Publication)
	require.NoError(t, err, "an exact historical retry must return its original result")
	require.Equal(t, before.Head, retried)
	current, err := fleet.CurrentHead(t.Context())
	require.NoError(t, err)
	require.Equal(t, after.Head, current)
	stale := fleetTestPublication(t, original.Publication.Grant, after.Head, "stale-owner-candidate")
	_, err = fleet.CommitPublication(t.Context(), stale)
	require.Error(t, err, "the old owner must not publish after live takeover")
}
