package app

import (
	"context"
	"github.com/agentstation/starmap/pkg/catalogs/permission"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/storage"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server"
	"github.com/stretchr/testify/require"
)

func applicationReceipt(t *testing.T, fence *authorization.Fence, reader authorization.RevisionReader, name string) authorization.Receipt {
	t.Helper()
	stamp, err := reader.Read(t.Context())
	require.NoError(t, err)
	ticket, err := fence.Start()
	require.NoError(t, err)
	now := time.Now()
	receipt, err := ticket.Accept(authorization.Evidence{Authority: name, Epoch: stamp.Epoch, Sequence: stamp.Sequence, VerifiedAt: now, ValidUntil: now.Add(5 * time.Minute)}, now, 5*time.Minute, 30*time.Second, true)
	require.NoError(t, err)
	return receipt
}

func TestApplicationOwnsAuthorizationFencesAndCatchup(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Identity.OAuth.GitHub = config.OAuthApplicationConfig{ClientID: "test-client", ClientSecret: "test-client-secret"}
	factories := explicitTestFactories()
	fakeHTTP := newBlockingHTTPRuntime()
	var dependencies server.Dependencies
	factories.newServer = func(_ *server.Config, value server.Dependencies) (httpRuntime, error) {
		dependencies = value
		return fakeHTTP, nil
	}
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	owner := application.authorization
	require.NotNil(t, owner)
	for _, status := range owner.monitor.Status() {
		require.Zero(t, status.Attempts, "construction starts no revision worker")
	}
	cases := []struct {
		name      string
		fence     *authorization.Fence
		reader    authorization.RevisionReader
		authority string
		mutate    func() error
	}{
		{name: "key", fence: owner.kv, reader: owner.kvRevision, authority: authorizationKV, mutate: func() error {
			record, err := dependencies.APIKeys.GetByID(t.Context(), testAPIKey().ID)
			if err != nil {
				return err
			}
			record.APIKey.Active = false
			_, err = dependencies.APIKeys.Update(t.Context(), record.APIKey, record.Revision)
			return err
		}},
		{name: "account", fence: owner.kv, reader: owner.kvRevision, authority: authorizationKV, mutate: func() error {
			record, err := dependencies.Accounts.GetByID(t.Context(), account.DefaultID)
			if err != nil {
				return err
			}
			record.Account.Name = "Updated account"
			_, err = dependencies.Accounts.Update(t.Context(), record.Account, record.Revision)
			return err
		}},
		{name: "identity", fence: owner.sql, reader: owner.sqlRevision, authority: authorizationSQL, mutate: func() error {
			_, err := dependencies.Identity.Teams.Create(t.Context(), identity.Team{ID: "new-team", Name: "New team"})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := applicationReceipt(t, tc.fence, tc.reader, tc.authority)
			require.NoError(t, tc.mutate())
			require.ErrorIs(t, old.Check(time.Now(), true), authorization.ErrWithdrawn, "local mutation must revoke before returning")
		})
	}
	retained := applicationReceipt(t, owner.kv, owner.kvRevision, authorizationKV)
	// An independent writer has no process-local hook. Durable polling must catch it.
	peer, err := apikey.Open(application.store)
	require.NoError(t, err)
	record, err := peer.GetByID(t.Context(), testAPIKey().ID)
	require.NoError(t, err)
	record.APIKey.Active = true
	_, err = peer.Update(t.Context(), record.APIKey, record.Revision)
	require.NoError(t, err)
	require.NoError(t, retained.Check(time.Now(), true))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	select {
	case <-fakeHTTP.started:
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP runtime did not start")
	}
	require.Eventually(t, func() bool { return retained.Check(time.Now(), true) != nil }, 5*time.Second, 10*time.Millisecond)
	stamp, err := revision.NewKV(application.store, nil).Read(t.Context())
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		for _, status := range owner.monitor.Status() {
			if status.Authority == authorizationKV {
				return status.Sequence == stamp.Sequence && status.Failure == ""
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("application workers did not stop")
	}
	attempts := owner.monitor.Status()[0].Attempts
	owner.monitor.Start(t.Context())
	require.Equal(t, attempts, owner.monitor.Status()[0].Attempts)
	_, err = owner.kv.Start()
	require.ErrorIs(t, err, authorization.ErrUnavailable)
}

type sampledCatalogRuntime struct {
	catalogRuntime
	sample atomic.Pointer[permission.ClockReading]
}

func (r *sampledCatalogRuntime) PermissionClock() permission.ClockReading {
	if sample := r.sample.Load(); sample != nil {
		return *sample
	}
	return permission.ClockReading{}
}

func TestSharedAuthorizationDoesNotRequireCatalogClock(t *testing.T) {
	factories := explicitTestFactories()
	openCatalog := factories.openCatalog
	sampled := &sampledCatalogRuntime{}
	factories.openCatalog = func(ctx context.Context, store storage.KVStore, settings runtimecatalog.Settings, lookup runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
		runtime, err := openCatalog(ctx, store, settings, lookup)
		sampled.catalogRuntime = runtime
		return sampled, err
	}
	var dependencies server.Dependencies
	factories.newServer = func(_ *server.Config, value server.Dependencies) (httpRuntime, error) {
		dependencies = value
		return newBlockingHTTPRuntime(), nil
	}
	cfg := validProductionConfig(t)
	cfg.Storage.Mode = "valkey"
	cfg.Storage.Valkey.URL = "redis://127.0.0.1:6379"
	cfg.Storage.Valkey.MaxConnections = 10
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.NotNil(t, dependencies.Authorization)
	require.NotNil(t, dependencies.PermissionClock)
	identity := authorization.Identity{Subject: testAPIKey().Hash}
	_, err = dependencies.Authorization.Resolve(t.Context(), identity)
	require.NoError(t, err)
	now := time.Now()
	sampled.sample.Store(&permission.ClockReading{Time: now, Known: true, Uncertainty: time.Second})
	bundle, err := dependencies.Authorization.Resolve(t.Context(), identity)
	require.NoError(t, err)
	current, healthy := dependencies.PermissionClock()
	require.NotEqual(t, current.Round(0), current)
	require.True(t, healthy)
	require.NoError(t, bundle.Permit().Check(current, healthy))
	for _, sample := range []permission.ClockReading{
		{Time: now, Known: false},
		{Time: now, Known: true, Uncertainty: 31 * time.Second},
		{Time: now, Known: true, Uncertainty: -time.Second},
		{Known: true},
	} {
		sampled.sample.Store(&sample)
		current, healthy = dependencies.PermissionClock()
		require.True(t, healthy)
		require.NoError(t, bundle.Permit().Check(current, healthy))
		_, err = dependencies.Authorization.Resolve(t.Context(), identity)
		require.NoError(t, err)
	}
}
