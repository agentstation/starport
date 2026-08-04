package router

import (
	"context"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestRouterUsesRoutableSnapshot(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	providerID, offering := firstRouterOffering(t, client.Catalog())
	require.NoError(t, plane.SetAdapter(routerAdapterAvailability(providerID, offering)))

	connector := connectors.NewMockConnector(connectors.ProviderConfig{})
	registry := &mockRegistry{connectors: map[string]connectors.Connector{
		string(providerID): connector,
	}}
	modelRouter := New(registry, WithCatalog(plane))

	modelID, selected, err := modelRouter.SelectModel(context.Background(), &Request{
		ChatRequest: &connectors.ChatRequest{Model: string(offering.DefinitionID)},
	})
	require.NoError(t, err)
	require.Same(t, connector, selected)
	require.Equal(t, string(providerID)+"/"+string(offering.ProviderModelID), modelID)

	t.Run("auto considers only snapshot routes", func(t *testing.T) {
		autoModelID, autoConnector, autoErr := modelRouter.SelectModel(context.Background(), &Request{
			ChatRequest: &connectors.ChatRequest{Model: AutoModelID},
		})
		require.NoError(t, autoErr)
		require.Same(t, connector, autoConnector)
		autoRoute, exists := plane.Current().ResolveRoute(autoModelID)
		require.True(t, exists)
		require.Equal(t, providerID, autoRoute.ProviderID)
	})

	t.Run("auto follows explicit model as a catalog fallback", func(t *testing.T) {
		autoModelID, autoConnector, autoErr := modelRouter.SelectModel(context.Background(), &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{"missing/model", AutoModelID},
			},
		})
		require.NoError(t, autoErr)
		require.Same(t, connector, autoConnector)
		_, exists := plane.Current().ResolveRoute(autoModelID)
		require.True(t, exists)
	})

	require.NoError(t, plane.RemoveAdapter(providerID))
	_, _, err = modelRouter.SelectModel(context.Background(), &Request{
		ChatRequest: &connectors.ChatRequest{Model: string(offering.DefinitionID)},
	})
	require.ErrorIs(t, err, ErrNoModelsAvailable)
}

func routerAdapterAvailability(
	providerID catalogs.ProviderID,
	offering catalogs.ProviderOffering,
) runtimecatalog.AdapterAvailability {
	types := make([]catalogs.EndpointType, 0, len(offering.Endpoints))
	for _, endpoint := range offering.Endpoints {
		types = append(types, endpoint.Type)
	}
	return runtimecatalog.AdapterAvailability{
		ProviderID:    providerID,
		Registered:    true,
		Configured:    true,
		Operations:    append([]catalogs.ProviderOperation(nil), offering.Service.Operations...),
		EndpointTypes: types,
	}
}

func firstRouterOffering(
	t *testing.T,
	source *catalogs.Catalog,
) (catalogs.ProviderID, catalogs.ProviderOffering) {
	t.Helper()
	for _, provider := range source.Providers().List() {
		offerings, err := source.ProviderOfferings(provider.ID)
		if err == nil && len(offerings) > 0 {
			return provider.ID, offerings[0]
		}
	}
	t.Fatal("Starmap embedded catalog has no provider offering")
	return "", catalogs.ProviderOffering{}
}
