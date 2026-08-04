package proxy

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
)

func TestOfferingCacheCapability(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	providerID, offering := firstCachePricedOffering(t, client.Catalog())
	types := make([]catalogs.EndpointType, 0, len(offering.Endpoints))
	for _, endpoint := range offering.Endpoints {
		types = append(types, endpoint.Type)
	}
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
		ProviderID:    providerID,
		Registered:    true,
		Configured:    true,
		Operations:    append([]catalogs.ProviderOperation(nil), offering.Service.Operations...),
		EndpointTypes: types,
	}))

	routeID := string(providerID) + "/" + string(offering.ProviderModelID)
	route, found := plane.Current().ResolveRoute(routeID)
	require.True(t, found)
	require.True(t, route.SupportsPromptCache())
	write, read, ok := cacheTokenPrices(plane, routeID)
	require.True(t, ok)
	require.Equal(t, modelTokenPrice(offering.Pricing.Tokens.CacheWrite), write)
	require.Equal(t, modelTokenPrice(offering.Pricing.Tokens.CacheRead), read)

	_, _, ok = cacheTokenPrices(nil, routeID)
	require.False(t, ok)
	_, _, ok = cacheTokenPrices(plane, "missing/model")
	require.False(t, ok)
}

func TestOfferingPriceHasNoFallback(t *testing.T) {
	require.Empty(t, formatTokenPrice(nil))
	_, _, ok := cacheTokenPrices(nil, "provider/model")
	require.False(t, ok)

	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	providerID, offering := firstOfferingWithoutCachePrice(t, client.Catalog())
	types := make([]catalogs.EndpointType, 0, len(offering.Endpoints))
	for _, endpoint := range offering.Endpoints {
		types = append(types, endpoint.Type)
	}
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
		ProviderID: providerID, Registered: true, Configured: true,
		Operations: offering.Service.Operations, EndpointTypes: types,
	}))
	_, _, ok = cacheTokenPrices(plane, string(providerID)+"/"+string(offering.ProviderModelID))
	require.False(t, ok)
}

func TestSnapshotOnlyDiscovery(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	guard := &catalogIOGuardConnector{Connector: connectors.NewMockConnector(connectors.ProviderConfig{})}
	providerRegistry := registry.NewEmptyWithCatalog(plane)
	require.NoError(t, providerRegistry.Register(string(catalogs.ProviderIDOpenAI), guard))
	require.NoError(t, providerRegistry.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, providerRegistry.Close()) })
	service := &proxy{registry: providerRegistry}

	models, err := service.ListModels(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, models.Data)
	endpoints, err := service.GetModelEndpoints(context.Background(), models.Data[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, endpoints.Endpoints)
	require.EqualValues(t, 0, atomic.LoadInt32(&guard.calls))
}

func TestModelDiscoveryRetainsOneCatalogGeneration(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	providerRegistry := registry.NewEmptyWithCatalog(plane)
	require.NoError(t, providerRegistry.Register(
		string(catalogs.ProviderIDOpenAI),
		connectors.NewMockConnector(connectors.ProviderConfig{}),
	))
	require.NoError(t, providerRegistry.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, providerRegistry.Close()) })

	retained := plane.Current()
	beforeRefresh := modelsResponseFromSnapshot(retained)
	require.NotEmpty(t, beforeRefresh.Data)

	require.NoError(t, plane.RemoveAdapter(catalogs.ProviderIDOpenAI))
	require.Empty(t, modelsResponseFromSnapshot(plane.Current()).Data)
	require.Equal(t, beforeRefresh, modelsResponseFromSnapshot(retained))
}

type catalogIOGuardConnector struct {
	connectors.Connector
	calls int32
}

func (c *catalogIOGuardConnector) Chat(
	ctx context.Context,
	req *connectors.ChatRequest,
) (*connectors.ChatResponse, error) {
	atomic.AddInt32(&c.calls, 1)
	return c.Connector.Chat(ctx, req)
}

func (c *catalogIOGuardConnector) ChatStream(
	ctx context.Context,
	req *connectors.ChatRequest,
) (connectors.ChatStream, error) {
	atomic.AddInt32(&c.calls, 1)
	return c.Connector.ChatStream(ctx, req)
}

func (c *catalogIOGuardConnector) Embeddings(
	ctx context.Context,
	req *connectors.EmbeddingsRequest,
) (*connectors.EmbeddingsResponse, error) {
	atomic.AddInt32(&c.calls, 1)
	return c.Connector.Embeddings(ctx, req)
}

func firstCachePricedOffering(
	t *testing.T,
	source *catalogs.Catalog,
) (catalogs.ProviderID, catalogs.ProviderOffering) {
	t.Helper()
	for _, provider := range source.Providers().List() {
		offerings, err := source.ProviderOfferings(provider.ID)
		if err != nil {
			continue
		}
		for _, offering := range offerings {
			if offering.Pricing == nil || offering.Pricing.Tokens == nil ||
				offering.Service.PromptCache == nil || !*offering.Service.PromptCache ||
				!offering.Supports(catalogs.ProviderOperationChatCompletions) || len(offering.Endpoints) == 0 {
				continue
			}
			if offering.Pricing.Tokens.CacheWrite != nil || offering.Pricing.Tokens.CacheRead != nil {
				return provider.ID, offering
			}
		}
	}
	t.Fatal("Starmap embedded catalog has no cache-priced offering")
	return "", catalogs.ProviderOffering{}
}

func firstOfferingWithoutCachePrice(
	t *testing.T,
	source *catalogs.Catalog,
) (catalogs.ProviderID, catalogs.ProviderOffering) {
	t.Helper()
	for _, provider := range source.Providers().List() {
		offerings, err := source.ProviderOfferings(provider.ID)
		if err != nil {
			continue
		}
		for _, offering := range offerings {
			if len(offering.Service.Operations) == 0 || len(offering.Endpoints) == 0 {
				continue
			}
			if offering.Pricing == nil || offering.Pricing.Tokens == nil ||
				(offering.Pricing.Tokens.CacheWrite == nil && offering.Pricing.Tokens.CacheRead == nil) {
				return provider.ID, offering
			}
		}
	}
	t.Fatal("Starmap embedded catalog has no offering without cache pricing")
	return "", catalogs.ProviderOffering{}
}
