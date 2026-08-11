package registry

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestOpenRequiresCatalogAndPermitsEmptyProviderSet(t *testing.T) {
	_, err := Open(nil, []Registration{{Provider: "openai"}})
	require.ErrorIs(t, err, ErrCatalogRequired)

	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	registry, err := Open(plane, nil)
	require.NoError(t, err)
	require.Empty(t, registry.ListProviders())
	require.NoError(t, registry.Close())
}

func TestOpenRegistersOnlyExplicitProviders(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	providerID, _ := firstCatalogOffering(t, client.Catalog())
	provider := string(providerID)
	providerConfig := connectors.ProviderConfig{BaseURL: "http://provider.test"}
	connector := connectors.NewMockConnector(providerConfig)

	registry, err := Open(plane, []Registration{{
		Provider: provider, Connector: connector, OperatorSource: registryTestMaterialSource{},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	assert.Equal(t, []string{provider}, registry.ListProviders())
	_, err = registry.Get("mock")
	require.Error(t, err)
}

func TestOpenRejectsIncompleteRegistration(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	_, err = Open(plane, []Registration{{Connector: connectors.NewMockConnector(connectors.ProviderConfig{})}})
	require.ErrorIs(t, err, ErrProviderRequired)
	_, err = Open(plane, []Registration{{Provider: "openai"}})
	require.ErrorIs(t, err, ErrConnectorRequired)
}

func TestOpenFailureClosesEveryConnectorOnce(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	first := newCloseTrackingConnector()
	duplicate := newCloseTrackingConnector()
	future := newCloseTrackingConnector()
	_, err = Open(plane, []Registration{
		{Provider: "openai", Connector: first, OperatorSource: registryTestMaterialSource{}},
		{Provider: "openai", Connector: duplicate, OperatorSource: registryTestMaterialSource{}},
		{Provider: "anthropic", Connector: future, OperatorSource: registryTestMaterialSource{}},
	})
	require.Error(t, err)
	require.Equal(t, int32(1), first.closeCount.Load())
	require.Equal(t, int32(1), duplicate.closeCount.Load())
	require.Equal(t, int32(1), future.closeCount.Load())
}

type registryTestMaterialSource struct{}

func (registryTestMaterialSource) ResolveMaterial(context.Context) (credentials.Material, error) {
	return credentials.NewMaterial(
		catalogs.ProviderCredentialProfile{ID: "none", Primitive: catalogs.ProviderAuthenticationNone},
		nil,
		credentials.MaterialMetadata{Version: "test"},
	), nil
}

func TestRegistryStartAndCloseLifecycle(t *testing.T) {
	registry := NewEmpty()
	providerConfig := connectors.ProviderConfig{BaseURL: "http://mock", Enabled: true}
	require.NoError(t, registry.Register("mock", connectors.NewMockConnector(providerConfig)))

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, registry.Start(ctx))
	require.NoError(t, registry.Start(ctx))
	require.ErrorIs(t, registry.Register("late", newCloseTrackingConnector()), ErrRegistryStarted)
	cancel()
	require.NoError(t, registry.Close())
	require.NoError(t, registry.Close())
	require.ErrorIs(t, registry.Start(context.Background()), ErrRegistryClosed)
	require.ErrorIs(t, registry.Register("closed", newCloseTrackingConnector()), ErrRegistryClosed)
}

func TestNewEmpty(t *testing.T) {
	registry := NewEmpty()
	require.NotNil(t, registry)
	assert.Empty(t, registry.ListProviders())
}

type closeTrackingConnector struct {
	connectors.Connector
	closeCount atomic.Int32
}

func newCloseTrackingConnector() *closeTrackingConnector {
	return &closeTrackingConnector{
		Connector: connectors.NewMockConnector(connectors.ProviderConfig{BaseURL: "http://mock"}),
	}
}

func (c *closeTrackingConnector) Close() error {
	c.closeCount.Add(1)
	return nil
}
