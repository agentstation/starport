package proxy

import (
	"context"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/catalog/disclosure"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/catalog/view"
	"github.com/stretchr/testify/require"
)

type withdrawingCatalogCache struct {
	*mockCacheManager
	onRead func()
}

func (m *withdrawingCatalogCache) GetModel(ctx context.Context, key string, target any) (bool, error) {
	found, err := m.mockCacheManager.GetModel(ctx, key, target)
	if m.onRead != nil {
		m.onRead()
	}
	return found, err
}

func TestCompatibilityDiscoveryRejectsWithdrawnAuthority(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	source := &cachePermissionSource{Client: client}
	source.allowed.Store(true)
	plane, err := runtimecatalog.Open(source)
	require.NoError(t, err)
	service := &proxy{registry: catalogDiscoveryRegistry{runtime: &catalogDiscoveryRuntime{snapshot: plane.Current()}}}
	source.allowed.Store(false)
	for _, operation := range []struct {
		name string
		read func(*testing.T) error
	}{
		{"models", func(t *testing.T) error {
			value, err := service.ListModels(t.Context())
			require.Nil(t, value)
			return err
		}},
		{"providers", func(t *testing.T) error {
			value, err := service.ListProviders(t.Context())
			require.Nil(t, value)
			return err
		}},
		{"endpoints", func(t *testing.T) error {
			value, err := service.GetModelEndpoints(t.Context(), "model")
			require.Nil(t, value)
			return err
		}},
		{"authors", func(t *testing.T) error {
			value, err := service.ListAuthors(t.Context())
			require.Nil(t, value)
			return err
		}},
		{"author", func(t *testing.T) error {
			value, err := service.GetAuthor(t.Context(), "author")
			require.Nil(t, value)
			return err
		}},
		{"logo", func(t *testing.T) error {
			value, err := service.GetLogo(t.Context(), view.LogoKindProviders, "provider")
			require.Nil(t, value)
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) { requireCatalogPermissionRefusal(t, operation.read(t)) })
	}
}

func TestDiscoveryCacheLookupRechecksAuthority(t *testing.T) {
	for _, operation := range []string{"models", "providers", "endpoints"} {
		t.Run(operation, func(t *testing.T) {
			client, err := starmap.New()
			require.NoError(t, err)
			source := &cachePermissionSource{Client: client}
			source.allowed.Store(true)
			plane, err := runtimecatalog.Open(source)
			require.NoError(t, err)
			manager := &withdrawingCatalogCache{mockCacheManager: newMockCacheManager()}
			upstream := &mockProxyImpl{modelsResponse: &ModelsResponse{Data: []ModelInfo{{ID: "retained-private-model"}}}, providersResponse: &ProvidersResponse{Providers: []ProviderInfo{{ID: "retained-private-provider"}}}}
			service := &cachedService{service: upstream, runtime: &cacheRuntimeSource{snapshot: plane.Current()}, cacheManager: manager, cacheConfig: CacheConfig{EnableModelCache: true, EnableProviderCache: true}}
			read := func() (bool, error) {
				switch operation {
				case "models":
					v, e := service.ListModels(t.Context())
					return v == nil, e
				case "providers":
					v, e := service.ListProviders(t.Context())
					return v == nil, e
				default:
					v, e := service.GetModelEndpoints(t.Context(), string(plane.Current().Catalog().Definitions()[0].ID))
					return v == nil, e
				}
			}
			empty, err := read()
			require.NoError(t, err)
			require.False(t, empty)
			require.Equal(t, 1, manager.calls["SetModel"])
			manager.onRead = func() { source.allowed.Store(false) }
			empty, err = read()
			require.True(t, empty)
			requireCatalogPermissionRefusal(t, err)
		})
	}
}

func TestCompatibilityProjectionRechecksAuthorityAtDelivery(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	source := &cachePermissionSource{Client: client}
	source.allowed.Store(true)
	plane, err := runtimecatalog.Open(source)
	require.NoError(t, err)
	provider, offering := firstDiscoveryOffering(t, client.Catalog())
	var endpointTypes []catalogs.EndpointType
	for _, endpoint := range offering.Endpoints {
		endpointTypes = append(endpointTypes, endpoint.Type)
	}
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{ProviderID: provider, Registered: true, Operations: offering.Service.Operations, EndpointTypes: endpointTypes}))
	projected := false
	runtime := &catalogDiscoveryRuntime{snapshot: plane.Current(), onRequiresAuthentication: func() { projected = true; source.allowed.Store(false) }}
	service := &proxy{registry: catalogDiscoveryRegistry{runtime: runtime}}
	response, err := service.ListProviders(t.Context())
	require.True(t, projected)
	require.Nil(t, response)
	requireCatalogPermissionRefusal(t, err)
}

func TestDiscoveryCacheSeparatesDisclosureMembership(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	provider, offering := firstDiscoveryOffering(t, client.Catalog())
	var endpointTypes []catalogs.EndpointType
	for _, endpoint := range offering.Endpoints {
		endpointTypes = append(endpointTypes, endpoint.Type)
	}
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{ProviderID: provider, Registered: true, Operations: offering.Service.Operations, EndpointTypes: endpointTypes}))
	snapshot := plane.Current()
	allowed := disclosure.New(snapshot, apikey.APIKey{}, account.Account{})
	denied := disclosure.Policy{}
	for _, kind := range []string{"models", "providers"} {
		t.Run(kind, func(t *testing.T) {
			manager := newMockCacheManager()
			upstream := &proxy{registry: catalogDiscoveryRegistry{runtime: &catalogDiscoveryRuntime{snapshot: snapshot}}}
			service := &cachedService{service: upstream, runtime: &cacheRuntimeSource{snapshot: snapshot}, cacheManager: manager, cacheConfig: CacheConfig{EnableModelCache: true, EnableProviderCache: true}}
			read := func(policy disclosure.Policy, visible bool) {
				ctx := disclosure.WithPolicy(t.Context(), policy)
				if kind == "models" {
					response, readErr := service.ListModels(ctx)
					require.NoError(t, readErr)
					require.Equal(t, visible, len(response.Data) > 0)
				} else {
					response, readErr := service.ListProviders(ctx)
					require.NoError(t, readErr)
					require.Equal(t, visible, len(response.Providers) > 0)
				}
				require.NoError(t, err)
			}
			read(allowed, true)
			require.Equal(t, 1, manager.calls["SetModel"])
			read(allowed, true)
			require.Equal(t, 1, manager.calls["SetModel"], "same membership must hit the serialized cache")
			read(denied, false)
			require.Equal(t, 2, manager.calls["SetModel"], "different membership must not reuse the first response")
			read(denied, false)
			require.Equal(t, 2, manager.calls["SetModel"])
		})
	}
}

func TestEndpointLookupDoesNotRevealDeniedMembership(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	_, offering := firstDiscoveryOffering(t, client.Catalog())
	snapshot := plane.Current()
	direct := &proxy{registry: catalogDiscoveryRegistry{runtime: &catalogDiscoveryRuntime{snapshot: snapshot}}}
	manager := newMockCacheManager()
	cached := &cachedService{service: direct, runtime: &cacheRuntimeSource{snapshot: snapshot}, cacheManager: manager, cacheConfig: CacheConfig{EnableModelCache: true}}
	ctx := disclosure.WithPolicy(t.Context(), disclosure.Policy{})
	for _, service := range []Proxy{direct, cached} {
		for _, name := range []string{string(offering.DefinitionID), "unknown/model"} {
			response, err := service.GetModelEndpoints(ctx, name)
			require.Nil(t, response)
			var refusal *ProviderError
			require.ErrorAs(t, err, &refusal)
			require.Equal(t, "not_found", refusal.Code)
			require.Equal(t, "Model not found", refusal.Message)
		}
	}
	require.Zero(t, manager.calls["GetModel"], "denied membership must not reach cache lookup")
}
