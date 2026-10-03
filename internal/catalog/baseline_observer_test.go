package catalog

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

// snapshot copies every live shared value.
func (s *memoryIncarnationStore) snapshot() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make(map[string][]byte, len(s.values))
	for key, value := range s.values {
		values[key] = bytes.Clone(value)
	}
	return values
}

// observe opens one baseline observer as OpenBaselineObserver does, without the recovery witness open.
// It keeps the collectors of the memory fleet gateways, because the replay binds the acquisition composition.
// It returns the observing session, which counts the refused shared writes.
func (f *memoryFleet) observe(t *testing.T, settings Settings) (*Runtime, *FleetStore) {
	t.Helper()
	observing := f.session()
	observer, err := observeFleet(t.Context(), observing, settings, func(session *FleetStore, observed Settings) (*Runtime, error) {
		return openRuntime(t.Context(), f.kv, observed, runtimeCollectors{fleet: session})
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	return observer, observing
}

// kvSnapshot copies every live value of a KV store.
func kvSnapshot(t *testing.T, kv storage.KVStore) map[string][]byte {
	t.Helper()
	keys, err := kv.Scan(t.Context(), "*", 0)
	require.NoError(t, err)
	values := make(map[string][]byte, len(keys))
	for _, key := range keys {
		value, err := kv.Get(t.Context(), key)
		require.NoError(t, err)
		values[key] = value
	}
	return values
}

// runtimeDirectoryLockName is the Starmap runtime directory lock file name (runtime/directory.go).
const runtimeDirectoryLockName = ".owner.lock"

// directorySnapshot records the mode, size, modification time, and content digest of every entry under root.
func directorySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	entries := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		digest := ""
		// The live leader holds the Starmap runtime directory lock. Windows locks the
		// file range for every other process, so the snapshot records the lock by its
		// metadata only.
		if info.Mode().IsRegular() && entry.Name() != runtimeDirectoryLockName {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest = payloadDigest(data)
		}
		entries[path] = fmt.Sprintf("%s %d %s %s", info.Mode(), info.Size(), info.ModTime().Format(time.RFC3339Nano), digest)
		return nil
	}))
	return entries
}

func TestBaselineObserverReadsWithoutTheLease(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	holder := fleet.leaseHolder(t, leader.fleet)
	before := fleet.store.snapshot()
	settings := fleet.settings(t)
	observer, _ := fleet.observe(t, settings)
	observed, err := observer.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, report.Retained, observed.Retained)
	require.Equal(t, report.Packaged, observed.Packaged)
	require.Equal(t, report.HeadRevision, observed.HeadRevision)
	require.True(t, observed.Promotable, observed.Refusal)
	require.NoDirExists(t, settings.StateDirectory, "the observer never opens the gateway state directory")
	require.NoError(t, observer.Close(ctx))
	require.NoDirExists(t, observer.temporary, "Close removes the observer directory")
	require.Equal(t, holder, fleet.leaseHolder(t, leader.fleet), "the observer leaves the leader in place")
	require.True(t, maps.EqualFunc(before, fleet.store.snapshot(), bytes.Equal), "the observer writes no shared key")

	// Without a leader the observer still never takes the lease.
	require.NoError(t, leader.Close(ctx))
	_, _, err = fleet.store.ReadWithLifetime(ctx, leader.fleet.prefix+"lease", 4096)
	require.ErrorIs(t, err, storage.ErrNotFound)
	alone, _ := fleet.observe(t, fleet.settings(t))
	aloneReport, err := alone.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, report.Retained, aloneReport.Retained)
	require.NoError(t, alone.Close(ctx))
	_, _, err = fleet.store.ReadWithLifetime(ctx, leader.fleet.prefix+"lease", 4096)
	require.ErrorIs(t, err, storage.ErrNotFound, "the observer takes no lease")
}

func TestBaselineObserverRefusesWithoutTheFleetReplay(t *testing.T) {
	fleet, leader, _ := promotableFleet(t)
	ctx := t.Context()
	holder := fleet.leaseHolder(t, leader.fleet)
	// The production composition adds acquisition collectors that the memory fleet gateways lack.
	observer, err := observeFleet(ctx, fleet.session(), fleet.settings(t), func(observing *FleetStore, observed Settings) (*Runtime, error) {
		return openRuntimeWithFleet(ctx, fleet.kv, observing, observed, nil)
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	_, err = observer.BaselineReport()
	require.ErrorContains(t, err, "the retained baseline is unknown", "a report never names the packaged baseline as retained")
	require.NoError(t, observer.Close(ctx))
	require.Equal(t, holder, fleet.leaseHolder(t, leader.fleet))
}

func TestBaselineObserverSettingsLeaveTheGatewayDirectories(t *testing.T) {
	gateway := Settings{
		StateDirectory: "/var/lib/starport/state", SourceCacheDirectory: "/var/cache/starport",
		BaselineDirectory: "/var/lib/starport/baseline", CredentialPolicyDirectory: "/var/lib/starport/policy",
		WorkspacePath: "/srv/catalog", DeploymentID: "deployment", InstanceID: "instance",
	}
	directory := t.TempDir()
	observed := gateway.observer(directory)
	for _, path := range []string{observed.StateDirectory, observed.SourceCacheDirectory, observed.BaselineDirectory} {
		relative, err := filepath.Rel(directory, path)
		require.NoError(t, err)
		require.True(t, filepath.IsLocal(relative), "%s must stay in the observer directory", path)
	}
	require.Empty(t, observed.CredentialPolicyDirectory, "the observer keeps no credential policy decision")
	require.Equal(t, gateway.WorkspacePath, observed.WorkspacePath, "the observer reads the operator workspace")
	require.Equal(t, gateway.DeploymentID, observed.DeploymentID)
}

// TestBaselineObserverReadsReadOnlyStorage opens an observer over read-only shared storage
// beside a gateway with the same settings. A write or a lease acquisition would fail.
// The memory fleet composition stands in for the gateway composition. The production
// composition needs published acquisition capabilities, so it is out of reach here.
func TestBaselineObserverReadsReadOnlyStorage(t *testing.T) {
	fleet, kv, witness, db := fleetTestStoreWithSQL(t)
	ctx := t.Context()
	deployment := fleet.identity.DeploymentID
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = deployment
	session := func() *FleetStore {
		store, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, deployment)
		require.NoError(t, err)
		return store
	}
	retained, _ := retainOlderBaseline(t, kv, session, settings)
	gateway, err := openRuntime(ctx, kv, settings, runtimeCollectors{fleet: session()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = gateway.Close(context.Background()) })
	expected, err := gateway.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, baselineIdentity(retained), expected.Retained)
	grant, err := kv.Get(ctx, fleet.prefix+"lease")
	require.NoError(t, err)

	readOnly, err := storage.OpenReadOnly(storage.Config{Type: storage.StorageTypeValkey, Valkey: storage.ValkeyConfig{DeploymentID: os.Getenv("CSP11_TEST_STORAGE_DEPLOYMENT"), URL: os.Getenv("TEST_VALKEY_URL")}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, readOnly.Close()) })
	observing, err := openRecoveryFleet(ctx, readOnly, db, deployment)
	require.NoError(t, err)
	observer, err := observeFleet(ctx, observing, settings, func(session *FleetStore, observed Settings) (*Runtime, error) {
		return openRuntime(ctx, readOnly, observed, runtimeCollectors{fleet: session})
	})
	require.NoError(t, err)
	directory := observer.temporary
	report, err := observer.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, expected, report, "the observer replays the retained baseline of the gateway")
	require.NoError(t, observer.Close(ctx))
	require.NoDirExists(t, directory)
	after, err := kv.Get(ctx, fleet.prefix+"lease")
	require.NoError(t, err)
	require.Equal(t, grant, after, "the observer leaves the gateway lease in place")
	require.Zero(t, observing.refusedWrites.Load(), "the observer sends no write to shared storage")
}

// TestBaselineObserverWritesNothingBesideALiveLeader opens a baseline observer with the
// settings of a live leader. The observer writes no shared, KV, or gateway directory state.
func TestBaselineObserverWritesNothingBesideALiveLeader(t *testing.T) {
	fleet := newMemoryFleet()
	ctx := t.Context()
	settings := fleet.settings(t)
	retained, _ := retainOlderBaseline(t, fleet.kv, fleet.session, settings)
	leader := fleet.openWith(t, settings)
	report, err := leader.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, baselineIdentity(retained), report.Retained)
	lease := fleet.heldLease(t, leader)
	head, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	gateway := filepath.Dir(settings.StateDirectory)
	shared, local, directory := fleet.store.snapshot(), kvSnapshot(t, fleet.kv), directorySnapshot(t, gateway)

	opened := time.Now()
	observer, observing := fleet.observe(t, settings)
	temporary := observer.temporary
	relative, err := filepath.Rel(filepath.Clean(os.TempDir()), temporary)
	require.NoError(t, err)
	require.True(t, filepath.IsLocal(relative), "the observer directory %s must be under the system temporary directory", temporary)
	require.True(t, strings.HasPrefix(filepath.Base(temporary), observerDirectoryPattern))
	observed, err := observer.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, report, observed, "the observer reports the retained baseline and the current head")
	require.Equal(t, head.Revision, observed.HeadRevision)
	require.NoError(t, observer.Close(ctx))
	t.Logf("observer open to close: %s", time.Since(opened))

	require.NoDirExists(t, temporary, "Close removes the observer directory")
	require.Zero(t, observing.refusedWrites.Load(), "the observer sends no write to shared storage")
	require.Equal(t, lease, fleet.heldLease(t, leader), "the leader still holds the lease")
	after, err := leader.fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, head, after, "the leader revision is unchanged")
	require.True(t, maps.EqualFunc(shared, fleet.store.snapshot(), bytes.Equal), "the observer writes no shared key")
	require.True(t, maps.EqualFunc(local, kvSnapshot(t, fleet.kv), bytes.Equal), "the observer writes no KV key")
	require.Equal(t, directory, directorySnapshot(t, gateway), "the observer leaves the gateway directory unchanged")
}

func TestBaselineObserverReportsNoFleetHead(t *testing.T) {
	fleet := newMemoryFleet()
	before, err := filepath.Glob(filepath.Join(os.TempDir(), observerDirectoryPattern+"*"))
	require.NoError(t, err)
	_, err = observeFleet(t.Context(), fleet.session(), fleet.settings(t), func(*FleetStore, Settings) (*Runtime, error) {
		require.FailNow(t, "a fleet without a head opens no runtime")
		return nil, nil
	})
	require.ErrorIs(t, err, ErrNoFleetHead)
	after, err := filepath.Glob(filepath.Join(os.TempDir(), observerDirectoryPattern+"*"))
	require.NoError(t, err)
	require.Equal(t, before, after, "a fleet without a head creates no observer directory")
	require.Empty(t, fleet.store.snapshot(), "a fleet without a head gets no write")
}
