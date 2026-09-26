package catalog

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/catalog/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func fleetTestStores(t *testing.T) (storage.KVStore, *recovery.Witness, *sqlstore.DB) {
	t.Helper()
	address, sqlURL := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL")
	if address == "" || sqlURL == "" {
		t.Skip("UNVERIFIED: real Valkey and PostgreSQL are required")
	}
	kv, err := storage.OpenValkey(storage.ValkeyConfig{URL: address})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, kv.Close()) })
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: sqlURL}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	witness, err := recovery.New(db)
	require.NoError(t, err)
	return kv, witness, db
}

func fleetTestStore(t *testing.T) (*FleetStore, storage.KVStore, *recovery.Witness) {
	t.Helper()
	kv, witness, db := fleetTestStores(t)
	provider := kv.(storage.IncarnationProvider)
	deployment := "fleet-test-" + rand.Text()
	t.Cleanup(func() {
		_, err := db.ExecContext(context.Background(), db.Bind("DELETE FROM catalog_recovery WHERE deployment_id = ?"), deployment)
		require.NoError(t, err)
	})
	closed, err := witness.Initialize(t.Context(), deployment)
	require.NoError(t, err)
	identity, err := provider.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	_, err = witness.Approve(t.Context(), closed, identity, "isolated-test-backend")
	require.NoError(t, err)
	fleet, err := NewFleetStore(t.Context(), provider, witness, deployment)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		keys, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
		require.NoError(t, err)
		require.NoError(t, kv.BatchDelete(ctx, keys))
	})
	return fleet, kv, witness
}

func fleetTestPublication(t *testing.T, grant runtime.Lease, previous runtime.FleetHead, name string) runtime.FleetPublication {
	t.Helper()
	generation := runtimeTestGeneration(t, name, testEmptyCatalog(t, catalogs.ProviderID(name)), time.Now().UTC())
	data := []byte(`{"retained_input":"` + name + `"}`)
	return runtime.FleetPublication{Generation: generation, Grant: grant, Expected: previous, Recovery: runtime.FleetRecovery{
		GenerationID: generation.Manifest.GenerationID, PayloadChecksum: generation.Manifest.Payload.Checksum, Data: data, Checksum: payloadDigest(data),
	}}
}

func TestFleetStoreNativePublication(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	ctx := t.Context()
	_, err := fleet.CurrentHead(ctx)
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
	grant, err := fleet.AcquireLease(ctx, "first", time.Minute)
	require.NoError(t, err)
	first := fleetTestPublication(t, grant, runtime.FleetHead{}, "first")
	head, err := fleet.CommitPublication(ctx, first)
	require.NoError(t, err)
	require.Equal(t, uint64(1), head.Revision)
	snapshot, err := fleet.CurrentPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, head, snapshot.Head)
	require.Equal(t, first.Recovery, snapshot.Publication.Recovery)
	require.Equal(t, grant, snapshot.Publication.Grant)
	follower, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, fleet.identity.DeploymentID)
	require.NoError(t, err)
	recovered, err := follower.CurrentPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, snapshot, recovered)
	_, err = follower.AcquireLease(ctx, "first", time.Minute)
	var configError *starmaperrors.ConfigError
	require.ErrorAs(t, err, &configError)
	_, err = follower.AcquireLease(ctx, "second", time.Minute)
	require.ErrorIs(t, err, starmaperrors.ErrConflict)
	renewed, err := fleet.Renew(ctx, grant, time.Minute)
	require.NoError(t, err)
	require.Equal(t, grant.Epoch, renewed.Epoch)
	// An input-only revision preserves the immutable generation and moves the recovery head.
	next := first
	next.Expected = head
	next.Recovery.Data = []byte(`{"retained_input":"changed"}`)
	next.Recovery.Checksum = payloadDigest(next.Recovery.Data)
	nextHead, err := fleet.CommitPublication(ctx, next)
	require.NoError(t, err)
	require.Equal(t, head.Revision+1, nextHead.Revision)
	require.Equal(t, head.GenerationID, nextHead.GenerationID)
	require.NoError(t, fleet.Release(ctx, grant))
	retry, err := fleet.CommitPublication(ctx, first)
	require.NoError(t, err)
	require.Equal(t, head, retry, "an exact completed retry retains its original result")
	current, err := fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, nextHead, current, "an old retry must not move the current head")
	altered := first
	altered.Grant.SessionID = "another-session"
	_, err = fleet.CommitPublication(ctx, altered)
	require.ErrorIs(t, err, starmaperrors.ErrConflict)
	takeover, err := follower.AcquireLease(ctx, "second", time.Minute)
	require.NoError(t, err)
	require.Greater(t, takeover.Epoch, grant.Epoch)
	require.NotEqual(t, grant.SessionID, takeover.SessionID)
	_, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, nextHead, "stale"))
	require.ErrorIs(t, err, starmaperrors.ErrConflict)
	// The independent witness can close the current backend without relying on KV data.
	approved, err := witness.Approved(ctx, fleet.identity.DeploymentID)
	require.NoError(t, err)
	_, err = witness.Close(ctx, approved)
	require.NoError(t, err)
	_, err = follower.CurrentHead(ctx)
	require.ErrorIs(t, err, recovery.ErrClosed)
	_, err = NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, fleet.identity.DeploymentID)
	require.ErrorIs(t, err, recovery.ErrClosed)
}

func TestFleetStoreRefusesExpiredGrant(t *testing.T) {
	fleet, _, _ := fleetTestStore(t)
	grant, err := fleet.AcquireLease(t.Context(), "expires", 40*time.Millisecond)
	require.NoError(t, err)
	request := fleetTestPublication(t, grant, runtime.FleetHead{}, "expired")
	require.Eventually(t, func() bool {
		_, _, err := fleet.store.ReadWithLifetime(t.Context(), fleet.prefix+"lease", 4096)
		return errors.Is(err, storage.ErrNotFound)
	}, 3*time.Second, 5*time.Millisecond)
	_, err = fleet.CommitPublication(t.Context(), request)
	require.ErrorIs(t, err, starmaperrors.ErrConflict)
	_, err = fleet.Renew(t.Context(), grant, time.Minute)
	require.ErrorIs(t, err, starmaperrors.ErrConflict)
	_, err = fleet.CurrentHead(t.Context())
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
}

func TestFleetStoreSeparateProcessTakeover(t *testing.T) {
	fleet, _, _ := fleetTestStore(t)
	grant, err := fleet.AcquireLease(t.Context(), "former", time.Minute)
	require.NoError(t, err)
	request := fleetTestPublication(t, grant, runtime.FleetHead{}, "stale-process")
	require.NoError(t, fleet.Release(t.Context(), grant))
	binary, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), binary, "-test.run=^TestFleetStoreProcessHelper$", "-test.v")
	command.Env = append(os.Environ(), "CSP11_FLEET_CHILD="+fleet.identity.DeploymentID)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "CSP11_TAKEOVER=")
	_, err = fleet.CommitPublication(t.Context(), request)
	require.ErrorIs(t, err, starmaperrors.ErrConflict)
}

func TestFleetStoreProcessHelper(t *testing.T) {
	deployment := os.Getenv("CSP11_FLEET_CHILD")
	if deployment == "" {
		return
	}
	kv, witness, _ := fleetTestStores(t)
	fleet, err := NewFleetStore(t.Context(), kv.(storage.IncarnationProvider), witness, deployment)
	require.NoError(t, err)
	grant, err := fleet.AcquireLease(t.Context(), "replacement", time.Minute)
	require.NoError(t, err)
	data, err := json.Marshal(grant)
	require.NoError(t, err)
	t.Log("CSP11_TAKEOVER=" + strings.TrimSpace(string(data)))
}

func TestFleetStoreMissingSelectedPublicationIsNotAnEmptyStore(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	grant, err := fleet.AcquireLease(t.Context(), "owner", time.Minute)
	require.NoError(t, err)
	head, err := fleet.CommitPublication(t.Context(), fleetTestPublication(t, grant, runtime.FleetHead{}, "selected"))
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(t.Context(), head, runtime.FleetHead{}))
	require.NoError(t, kv.Delete(t.Context(), fleet.publicationKey(head)))
	_, err = fleet.CurrentPublication(t.Context())
	require.Error(t, err)
	require.NotErrorIs(t, err, starmaperrors.ErrNotFound, "missing selected content must not become an embedded bootstrap")
	_, err = fleet.AcceptedPublication(t.Context())
	require.Error(t, err)
	require.NotErrorIs(t, err, starmaperrors.ErrNotFound, "a missing accepted publication must not become an embedded bootstrap")
}

func TestFleetStoreAcceptanceUsesOriginalGrant(t *testing.T) {
	fleet, _, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "owner", time.Minute)
	require.NoError(t, err)
	first := fleetTestPublication(t, grant, runtime.FleetHead{}, "first-routable")
	firstHead, err := fleet.CommitPublication(ctx, first)
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(ctx, firstHead, runtime.FleetHead{}))
	require.NoError(t, fleet.Release(ctx, grant))
	require.NoError(t, fleet.AcceptPublication(ctx, firstHead, runtime.FleetHead{}), "a retry of current acceptance needs no new permission")
	grant, err = fleet.AcquireLease(ctx, "owner", time.Minute)
	require.NoError(t, err)
	second := fleetTestPublication(t, grant, firstHead, "second-routable")
	secondHead, err := fleet.CommitPublication(ctx, second)
	require.NoError(t, err)
	require.NoError(t, fleet.Release(ctx, grant))
	_, err = fleet.AcquireLease(ctx, "owner", time.Minute)
	require.NoError(t, err)
	require.ErrorIs(t, fleet.AcceptPublication(ctx, secondHead, firstHead), starmaperrors.ErrConflict, "a new lease must not relabel earlier validation work")
	accepted, err := fleet.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, firstHead, accepted.Head)
	history, err := fleet.AcceptedHistory(ctx)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, firstHead.GenerationID, history[0].GenerationID)
}

func TestFleetStoreNamespaceAndMalformedDescriptor(t *testing.T) {
	first, _, _ := fleetTestStore(t)
	second, _, _ := fleetTestStore(t)
	require.NotEqual(t, first.prefix, second.prefix)
	grant, err := first.AcquireLease(t.Context(), "same-holder", time.Minute)
	require.NoError(t, err)
	_, err = second.AcquireLease(t.Context(), "same-holder", time.Minute)
	require.NoError(t, err)
	head, err := first.CommitPublication(t.Context(), fleetTestPublication(t, grant, runtime.FleetHead{}, "isolated"))
	require.NoError(t, err)
	_, err = second.CurrentPublication(t.Context())
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
	_, err = second.Publication(t.Context(), head)
	require.ErrorIs(t, err, starmaperrors.ErrConflict)
	key := first.publicationKey(head)
	original, _, err := first.store.ReadWithLifetime(t.Context(), key, fleetDescriptorMaxBytes)
	require.NoError(t, err)
	var descriptor fleetBlob
	require.NoError(t, json.Unmarshal(original, &descriptor))
	descriptor.Record.Size = fleetEncodedMaxBytes + 1
	invalid, err := json.Marshal(descriptor)
	require.NoError(t, err)
	require.NoError(t, first.store.CompareAndSwap(t.Context(), []storage.CompareAndSwapMutation{{Key: key, ExpectedValue: original, NewValue: invalid}}))
	_, err = first.CurrentPublication(t.Context())
	require.Error(t, err)
	require.NotErrorIs(t, err, starmaperrors.ErrNotFound)
}

// expireBeforeSelection preserves native CAS and injects expiry after publication preparation.
type expireBeforeSelection struct {
	storage.IncarnationStore
	target string
	expire func(context.Context) error
	called bool
}

func (s *expireBeforeSelection) CompareAndSwap(ctx context.Context, changes []storage.CompareAndSwapMutation, live ...string) error {
	for _, change := range changes {
		if change.Key == s.target && !s.called {
			s.called = true
			if err := s.expire(ctx); err != nil {
				return err
			}
		}
	}
	return s.IncarnationStore.CompareAndSwap(ctx, changes, live...)
}

func TestFleetStoreLeaseExpiresDuringSelection(t *testing.T) {
	for _, operation := range []string{"publication", "acceptance"} {
		t.Run(operation, func(t *testing.T) {
			fleet, kv, _ := fleetTestStore(t)
			ctx := t.Context()
			grant, err := fleet.AcquireLease(ctx, "owner", time.Minute)
			require.NoError(t, err)
			first := fleetTestPublication(t, grant, runtime.FleetHead{}, "first")
			head, err := fleet.CommitPublication(ctx, first)
			require.NoError(t, err)
			require.NoError(t, fleet.AcceptPublication(ctx, head, runtime.FleetHead{}))
			next := fleetTestPublication(t, grant, head, "next")
			expiring := &expireBeforeSelection{IncarnationStore: fleet.store, target: fleet.prefix + "head",
				expire: func(ctx context.Context) error {
					return kv.ExpireAt(ctx, fleet.prefix+"lease", time.Now().Add(-time.Second))
				}}
			if operation == "publication" {
				fleet.store = expiring
				_, err = fleet.CommitPublication(ctx, next)
				require.ErrorIs(t, err, starmaperrors.ErrConflict)
				current, err := fleet.CurrentPublication(ctx)
				require.NoError(t, err)
				require.Equal(t, head, current.Head)
				require.Equal(t, first.Recovery, current.Publication.Recovery)
			} else {
				nextHead, err := fleet.CommitPublication(ctx, next)
				require.NoError(t, err)
				expiring.target = fleet.prefix + "accepted"
				fleet.store = expiring
				require.ErrorIs(t, fleet.AcceptPublication(ctx, nextHead, head), starmaperrors.ErrConflict)
			}
			require.True(t, expiring.called, "the failure must occur at native selection")
			accepted, err := fleet.AcceptedPublication(ctx)
			require.NoError(t, err)
			require.Equal(t, head, accepted.Head)
			history, err := fleet.AcceptedHistory(ctx)
			require.NoError(t, err)
			require.Len(t, history, 1)
		})
	}
}

func TestFleetStoreLostHeadCannotBootstrapAgain(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	grant, err := fleet.AcquireLease(t.Context(), "owner", time.Minute)
	require.NoError(t, err)
	head, err := fleet.CommitPublication(t.Context(), fleetTestPublication(t, grant, runtime.FleetHead{}, "initialized"))
	require.NoError(t, err)
	require.NoError(t, fleet.Release(t.Context(), grant))
	require.NoError(t, kv.Delete(t.Context(), fleet.prefix+"head"))
	_, err = fleet.CurrentHead(t.Context())
	require.Error(t, err)
	require.NotErrorIs(t, err, starmaperrors.ErrNotFound, "loss of the selected head must require recovery")
	_, err = fleet.CurrentPublication(t.Context())
	require.Error(t, err)
	require.NotErrorIs(t, err, starmaperrors.ErrNotFound)
	_, err = fleet.AcquireLease(t.Context(), "replacement", time.Minute)
	require.Error(t, err, "missing head must not permit fresh acquisition")
	retained, err := fleet.Publication(t.Context(), head)
	require.NoError(t, err, "recovery must retain the previous publication")
	require.Equal(t, head, retained.Head)
}
