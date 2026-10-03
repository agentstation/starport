package catalog

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"

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
func (f *memoryFleet) observe(t *testing.T, settings Settings) *Runtime {
	t.Helper()
	observing := f.session()
	observing.observe = true
	directory, err := os.MkdirTemp(t.TempDir(), "observer-")
	require.NoError(t, err)
	observer, err := openRuntime(t.Context(), f.kv, settings.observer(directory), runtimeCollectors{fleet: observing})
	require.NoError(t, err)
	observer.temporary = directory
	return observer
}

func TestBaselineObserverReadsWithoutTheLease(t *testing.T) {
	fleet, leader, report := promotableFleet(t)
	ctx := t.Context()
	holder := fleet.leaseHolder(t, leader.fleet)
	before := fleet.store.snapshot()
	settings := fleet.settings(t)
	observer := fleet.observe(t, settings)
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
	alone := fleet.observe(t, fleet.settings(t))
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
	observing := fleet.session()
	observing.observe = true
	directory, err := os.MkdirTemp(t.TempDir(), "observer-")
	require.NoError(t, err)
	// The production composition adds acquisition collectors that the memory fleet gateways lack.
	observer, err := openRuntimeWithFleet(ctx, fleet.kv, observing, fleet.settings(t).observer(directory), nil)
	require.NoError(t, err)
	observer.temporary = directory
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
	observing.observe = true
	directory, err := os.MkdirTemp(t.TempDir(), "observer-")
	require.NoError(t, err)
	observer, err := openRuntime(ctx, readOnly, settings.observer(directory), runtimeCollectors{fleet: observing})
	require.NoError(t, err)
	observer.temporary = directory
	report, err := observer.BaselineReport()
	require.NoError(t, err)
	require.Equal(t, expected, report, "the observer replays the retained baseline of the gateway")
	require.NoError(t, observer.Close(ctx))
	require.NoDirExists(t, directory)
	after, err := kv.Get(ctx, fleet.prefix+"lease")
	require.NoError(t, err)
	require.Equal(t, grant, after, "the observer leaves the gateway lease in place")
}
