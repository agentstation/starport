package proxy

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
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
		Operations:    append([]catalogs.ProviderOperation(nil), offering.Service.Operations...),
		EndpointTypes: types,
	}))

	routeID := string(providerID) + "/" + string(offering.ProviderModelID)
	route, found := plane.Current().ResolveRoute(routeID)
	require.True(t, found)
	require.True(t, route.SupportsPromptCache())
	write, read, ok := cacheTokenPrices(plane.Current(), routeID)
	require.True(t, ok)
	require.Equal(t, modelTokenPrice(offering.Pricing.Tokens.CacheWrite), write)
	require.Equal(t, modelTokenPrice(offering.Pricing.Tokens.CacheRead), read)

	_, _, ok = cacheTokenPrices(nil, routeID)
	require.False(t, ok)
	_, _, ok = cacheTokenPrices(plane.Current(), "missing/model")
	require.False(t, ok)
}

func TestOfferingPriceHasNoFallback(t *testing.T) {
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
		ProviderID: providerID, Registered: true,
		Operations: offering.Service.Operations, EndpointTypes: types,
	}))
	_, found := plane.Current().ResolveRoute(string(providerID) + "/" + string(offering.ProviderModelID))
	require.True(t, found)
	_, _, ok = cacheTokenPrices(plane.Current(), string(providerID)+"/"+string(offering.ProviderModelID))
	require.False(t, ok)
}

func TestSnapshotOnlyDiscovery(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	providerID, offering := firstDiscoveryOffering(t, client.Catalog())
	endpointTypes := make([]catalogs.EndpointType, 0, len(offering.Endpoints))
	for _, endpoint := range offering.Endpoints {
		endpointTypes = append(endpointTypes, endpoint.Type)
	}
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
		ProviderID: providerID, Registered: true,
		Operations: offering.Service.Operations, EndpointTypes: endpointTypes,
	}))
	runtime := &catalogDiscoveryRuntime{snapshot: plane.Current()}
	service := &proxy{registry: catalogDiscoveryRegistry{runtime: runtime}}

	models, err := service.ListModels(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, models.Data)
	endpoints, err := service.GetModelEndpoints(context.Background(), models.Data[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, endpoints.Endpoints)
	providers, err := service.ListProviders(context.Background())
	require.NoError(t, err)
	require.Len(t, providers.Providers, 1)
	require.Equal(t, string(providerID), providers.Providers[0].ID)
	require.NotEmpty(t, providers.Providers[0].Name)
	require.NotEmpty(t, providers.Providers[0].Models)
	require.NotEmpty(t, providers.Providers[0].Capabilities)
	require.True(t, providers.Providers[0].RequiresAuth)
	catalogProvider, err := client.Catalog().Provider(providerID)
	require.NoError(t, err)
	if contract := catalogProvider.Credentials; contract != nil && len(contract.Inference.Alternatives) > 0 {
		require.NotEmpty(t, providers.Providers[0].CredentialFields)
		for _, field := range providers.Providers[0].CredentialFields {
			require.NotEmpty(t, field.ID)
			require.Contains(t, []string{"secret", "parameter"}, field.Kind)
		}
	}
	require.EqualValues(t, 0, runtime.getCalls.Load())
}

func TestModelDiscoveryRetainsOneCatalogGeneration(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	providerID, offering := firstDiscoveryOffering(t, client.Catalog())
	endpointTypes := make([]catalogs.EndpointType, 0, len(offering.Endpoints))
	for _, endpoint := range offering.Endpoints {
		endpointTypes = append(endpointTypes, endpoint.Type)
	}
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
		ProviderID: providerID, Registered: true,
		Operations: offering.Service.Operations, EndpointTypes: endpointTypes,
	}))

	retained := plane.Current()
	beforeRefresh := modelsResponseFromSnapshot(retained)
	require.NotEmpty(t, beforeRefresh.Data)

	require.NoError(t, plane.RemoveAdapter(providerID))
	require.Empty(t, modelsResponseFromSnapshot(plane.Current()).Data)
	require.Equal(t, beforeRefresh, modelsResponseFromSnapshot(retained))
}

type catalogDiscoveryRegistry struct{ runtime *catalogDiscoveryRuntime }

func (r catalogDiscoveryRegistry) AcquireRuntime() (connectors.RuntimeLease, error) {
	return r.runtime, nil
}

type catalogDiscoveryRuntime struct {
	snapshot *runtimecatalog.RoutableSnapshot
	getCalls atomic.Int32
}

func (r *catalogDiscoveryRuntime) Snapshot() *runtimecatalog.RoutableSnapshot { return r.snapshot }
func (r *catalogDiscoveryRuntime) Get(string) connectors.Connector {
	r.getCalls.Add(1)
	return nil
}
func (*catalogDiscoveryRuntime) RequiresAuthentication(string) bool { return true }
func (*catalogDiscoveryRuntime) ResolveMaterial(
	context.Context,
	string,
) (credentials.Material, error) {
	return credentials.Material{}, nil
}
func (*catalogDiscoveryRuntime) Release() {}

func firstDiscoveryOffering(
	t *testing.T,
	source *catalogs.Catalog,
) (catalogs.ProviderID, catalogs.ProviderOffering) {
	t.Helper()
	for _, provider := range source.Providers().List() {
		offerings, err := source.ProviderOfferings(provider.ID)
		require.NoError(t, err)
		for _, offering := range offerings {
			if len(offering.Endpoints) > 0 && len(offering.Service.Operations) > 0 {
				return provider.ID, offering
			}
		}
	}
	t.Fatal("Starmap embedded catalog has no routable offering")
	return "", catalogs.ProviderOffering{}
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
