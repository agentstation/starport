package router

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/stretchr/testify/require"
)

func embeddedCatalogRouter(t testing.TB) (ModelRouter, string, int) {
	t.Helper()
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	var registrations []registry.Registration
	for _, provider := range client.Catalog().Providers().List() {
		offerings, err := client.Catalog().ProviderOfferings(provider.ID)
		require.NoError(t, err)
		var types []catalogs.EndpointType
		for _, offering := range offerings {
			for _, endpoint := range offering.Endpoints {
				types = append(types, endpoint.Type)
			}
		}
		registrations = append(registrations, registry.Registration{
			Provider: string(provider.ID), Connector: &mockConnector{name: string(provider.ID)},
			Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: types,
		})
	}
	runtimeRegistry, err := registry.Open(plane, registrations)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtimeRegistry.Close()) })
	routes := plane.Current().Routes()
	require.NotEmpty(t, routes)
	return New(&endpointBindingRegistry{registry: runtimeRegistry}, WithCatalog(plane)), routes[0].ID(), len(routes)
}

func TestEmbeddedCatalogSelectionProfile(t *testing.T) {
	router, model, routes := embeddedCatalogRouter(t)
	request := &Request{ChatRequest: &connectors.ChatRequest{Model: model}}
	allocations := testing.AllocsPerRun(20, func() {
		selected, connector, err := router.SelectModel(t.Context(), request)
		if err != nil || selected != model || connector == nil {
			t.Fatalf("selection %q: %v", selected, err)
		}
	})
	t.Logf("embedded exact offering: routes=%d, model=%s, allocations=%.0f", routes, model, allocations)
	require.LessOrEqual(t, allocations, float64(1500), "catalog selection alone must fit the complete request allocation budget")
}

func BenchmarkEmbeddedCatalogSelection(b *testing.B) {
	router, model, routes := embeddedCatalogRouter(b)
	request := &Request{ChatRequest: &connectors.ChatRequest{Model: model}}
	b.ReportAllocs()
	for b.Loop() {
		selected, connector, err := router.SelectModel(b.Context(), request)
		if err != nil || selected != model || connector == nil {
			b.Fatalf("selection %q: %v", selected, err)
		}
	}
	b.ReportMetric(float64(routes), "catalog-routes")
}
