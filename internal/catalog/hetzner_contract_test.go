package catalog

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedStarmapProjectsHetznerRoutes(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)

	plane, err := Open(client)
	require.NoError(t, err)

	snapshot := plane.Current()
	provider, err := snapshot.Catalog().Provider(catalogs.ProviderIDHetzner)
	require.NoError(t, err)
	require.Equal(t, "Hetzner", provider.Name)

	offerings, err := snapshot.Catalog().ProviderOfferings(catalogs.ProviderIDHetzner)
	require.NoError(t, err)
	require.Len(t, offerings, 2)

	require.NoError(t, plane.ReplaceAdapters([]AdapterAvailability{
		testAdapterAvailability(catalogs.ProviderIDHetzner, offerings[0], true),
	}))

	wantDefinitions := map[catalogs.ProviderModelID]catalogs.ModelDefinitionID{
		"Qwen/Qwen3.6-35B-A3B-FP8": "qwen/qwen3.6-35b-a3b",
		"Qwen3.8-27B":              "qwen/qwen3.8-27b",
	}
	routes := plane.Current().RoutesForProvider(catalogs.ProviderIDHetzner)
	require.Len(t, routes, len(wantDefinitions))
	for _, route := range routes {
		require.Equal(t, wantDefinitions[route.ProviderModelID], route.DefinitionID)
		require.True(t, route.Supports(catalogs.ProviderOperationChatCompletions))
		endpoint, found := route.Endpoint(catalogs.ProviderOperationChatCompletions)
		require.True(t, found)
		require.Equal(t, catalogs.EndpointTypeOpenAI, endpoint.Type)
		require.Equal(
			t,
			"https://inference.hetzner.com/api/v1/chat/completions",
			endpoint.URL,
		)
		delete(wantDefinitions, route.ProviderModelID)
	}
	require.Empty(t, wantDefinitions)
}
