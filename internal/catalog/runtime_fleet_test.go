package catalog

import (
	"context"
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
	settings.DeploymentID = "unapproved-" + t.Name()
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
	fleet, err := NewFleetStore(t.Context(), provider, witness, settings.DeploymentID)
	require.NoError(t, err)
	grant, err := fleet.AcquireLease(t.Context(), "publisher", time.Minute)
	require.NoError(t, err)
	publication := fleetTestPublication(t, grant, runtime.FleetHead{}, "diagnostic")
	head, err := fleet.CommitPublication(t.Context(), publication)
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(t.Context(), head, runtime.FleetHead{}))
	readOnly, err := storage.OpenReadOnly(storage.Config{Type: storage.StorageTypeValkey, Valkey: storage.ValkeyConfig{URL: os.Getenv("TEST_VALKEY_URL")}})
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
