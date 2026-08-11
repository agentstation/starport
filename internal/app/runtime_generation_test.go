package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	pkgsync "github.com/agentstation/starmap/pkg/sync"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
)

func TestAppRefreshPublishesCompleteRuntimeGeneration(t *testing.T) {
	fixture := newRuntimeRefreshFixture(t)
	oldLease, err := fixture.registry.AcquireRuntime()
	require.NoError(t, err)
	oldGenerationID := oldLease.Snapshot().GenerationID()

	require.NoError(t, fixture.application.refreshRuntime(t.Context()))
	newLease, err := fixture.registry.AcquireRuntime()
	require.NoError(t, err)
	defer newLease.Release()
	require.NotEqual(t, oldGenerationID, newLease.Snapshot().GenerationID())
	require.Equal(t, fixture.runtime.state.GenerationID, newLease.Snapshot().GenerationID())
	require.Same(t, fixture.newConnector, newLease.Get("openai"))
	require.Equal(t, int32(0), fixture.oldConnector.closed.Load())

	oldLease.Release()
	require.Equal(t, int32(1), fixture.oldConnector.closed.Load())
}

func TestAppRefreshFailureRetainsPriorRuntimeGeneration(t *testing.T) {
	fixture := newRuntimeRefreshFixture(t)
	before := fixture.registry.Snapshot()
	fixture.application.newConnector = func(
		string,
		[]catalogs.EndpointType,
		connectors.ProviderConfig,
	) (connectors.Connector, error) {
		return nil, errors.New("connector construction failed")
	}

	err := fixture.application.refreshRuntime(t.Context())
	require.ErrorContains(t, err, "connector construction failed")
	require.Same(t, before, fixture.registry.Snapshot())
	current, err := fixture.registry.Get("openai")
	require.NoError(t, err)
	require.Same(t, fixture.oldConnector, current)
	require.Equal(t, int32(0), fixture.oldConnector.closed.Load())
}

type runtimeRefreshFixture struct {
	application  *App
	runtime      *runtimeSyncFixture
	registry     *registry.Registry
	oldConnector *runtimeRefreshConnector
	newConnector *runtimeRefreshConnector
}

func newRuntimeRefreshFixture(t *testing.T) runtimeRefreshFixture {
	t.Helper()
	cfg, err := config.NewLoader().
		WithEnvironment(map[string]string{"OPENAI_API_KEY": "sk-runtime-refresh-test"}).
		Load(t.Context())
	require.NoError(t, err)
	cfg.Catalog.RefreshTimeout = time.Second
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	state := client.CurrentCatalogState()
	state.GenerationID += "-replacement"
	state.Sequence++
	resolved, err := cfg.ResolveProviderSet(
		t.Context(),
		plane.Current().Catalog().Providers(),
		config.ProvidersConfig{},
	)
	require.NoError(t, err)
	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)
	oldConnector := &runtimeRefreshConnector{Connector: connectors.NewMockConnector(connectors.ProviderConfig{})}
	registrations, err := buildRegistrations(
		plane.Current().Catalog(),
		transports,
		authentication,
		providers.Configurations(resolved),
		func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error) {
			return oldConnector, nil
		},
	)
	require.NoError(t, err)
	runtimeRegistry, err := registry.Open(plane, registrations)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtimeRegistry.Close()) })
	newConnector := &runtimeRefreshConnector{Connector: connectors.NewMockConnector(connectors.ProviderConfig{})}
	runtime := &runtimeSyncFixture{plane: plane, state: state}
	application := &App{
		config: cfg, providerSettings: config.ProvidersConfig{},
		catalogRuntime: runtime, catalog: plane, registry: runtimeRegistry,
		transports: transports, authentication: authentication,
		newConnector: func(
			string,
			[]catalogs.EndpointType,
			connectors.ProviderConfig,
		) (connectors.Connector, error) {
			return newConnector, nil
		},
	}
	return runtimeRefreshFixture{
		application: application, runtime: runtime, registry: runtimeRegistry,
		oldConnector: oldConnector, newConnector: newConnector,
	}
}

type runtimeSyncFixture struct {
	plane *runtimecatalog.ControlPlane
	state starmap.CatalogState
}

func (r *runtimeSyncFixture) ControlPlane() *runtimecatalog.ControlPlane { return r.plane }

func (r *runtimeSyncFixture) Sync(
	context.Context,
	...pkgsync.Option,
) (*pkgsync.Result, starmap.CatalogState, error) {
	return &pkgsync.Result{GenerationID: r.state.GenerationID}, r.state, nil
}

type runtimeRefreshConnector struct {
	connectors.Connector
	closed atomic.Int32
}

func (c *runtimeRefreshConnector) Close() error {
	c.closed.Add(1)
	return nil
}
