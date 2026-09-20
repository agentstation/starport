package proxy

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/cache"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

type discoveryCacheSource struct{ state starmap.CatalogState }

func (s discoveryCacheSource) CurrentCatalogState() starmap.CatalogState { return s.state }

type discoveryCacheObserver struct {
	*cache.Manager
	key    string
	writes int
}

func (c *discoveryCacheObserver) SetModel(ctx context.Context, key string, value any) error {
	c.key = key
	c.writes++
	return c.Manager.SetModel(ctx, key, value)
}

func TestDiscoveryCacheTracksAdapterRevision(t *testing.T) {
	builder := catalogs.NewEmpty()
	author := catalogs.Author{ID: "author", Name: "Author"}
	require.NoError(t, builder.SetAuthor(author))
	features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "model", Name: "Model", Authors: []catalogs.Author{author}, Features: features}))
	require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "provider", Name: "Provider", Inference: &catalogs.ProviderInference{BaseURL: "https://provider.test/v1", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeOpenAI, Path: "/chat/completions"}}}, Models: map[string]*catalogs.Model{"model": {ID: "model", ModelRef: "author/model", Name: "Model", Status: catalogs.ModelStatusActive, Features: features}}}))
	catalog, err := builder.Build()
	require.NoError(t, err)
	source := discoveryCacheSource{starmap.CatalogState{Catalog: catalog, GenerationID: "fixed-generation", Sequence: 1}}
	plane, err := runtimecatalog.Open(source)
	require.NoError(t, err)
	adapter := runtimecatalog.AdapterAvailability{ProviderID: "provider", Registered: true, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}}
	require.NoError(t, plane.SetAdapter(adapter))
	active := plane.Current()
	require.NoError(t, plane.RemoveAdapter("provider"))
	removed := plane.Current()
	require.NoError(t, plane.SetAdapter(adapter))
	restored := plane.Current()
	replica, err := runtimecatalog.Open(source)
	require.NoError(t, err)
	require.NoError(t, replica.SetAdapter(runtimecatalog.AdapterAvailability{ProviderID: "provider", Registered: false}))
	require.Equal(t, active.AvailabilityRevision(), replica.Current().AvailabilityRevision())
	for _, kind := range []string{"models", "providers", "endpoints"} {
		t.Run(kind, func(t *testing.T) {
			manager, err := cache.NewCacheManager(cache.ManagerConfig{}, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, manager.Close()) })
			observer := &discoveryCacheObserver{Manager: manager}
			read := func(t *testing.T, service Proxy) any {
				t.Helper()
				var value any
				var err error
				switch kind {
				case "models":
					value, err = service.ListModels(t.Context())
				case "providers":
					value, err = service.ListProviders(t.Context())
				default:
					value, err = service.GetModelEndpoints(t.Context(), "author/model")
				}
				require.NoError(t, err)
				return value
			}
			for _, phase := range []struct {
				name     string
				snapshot *runtimecatalog.RoutableSnapshot
			}{{"active", active}, {"removed", removed}, {"restored", restored}, {"independent replica", replica.Current()}} {
				t.Run(phase.name, func(t *testing.T) {
					require.Equal(t, "fixed-generation", phase.snapshot.GenerationID())
					direct := &proxy{registry: catalogDiscoveryRegistry{runtime: &catalogDiscoveryRuntime{snapshot: phase.snapshot}}}
					cached := &cachedService{service: direct, runtime: catalogDiscoveryRegistry{runtime: &catalogDiscoveryRuntime{snapshot: phase.snapshot}}, cacheManager: observer, cacheConfig: CacheConfig{EnableModelCache: true, EnableProviderCache: true}}
					expected, err := json.Marshal(read(t, direct))
					require.NoError(t, err)
					first, err := json.Marshal(read(t, cached))
					require.NoError(t, err)
					require.JSONEq(t, string(expected), string(first), "cache must match the current adapter snapshot")
					require.Eventually(t, func() bool {
						var value any
						found, err := manager.GetModel(t.Context(), observer.key, &value)
						return err == nil && found
					}, time.Second, time.Millisecond)
					writes := observer.writes
					warm, err := json.Marshal(read(t, cached))
					require.NoError(t, err)
					require.JSONEq(t, string(expected), string(warm))
					require.Equal(t, writes, observer.writes, "warm discovery must use the real serialized cache")
				})
			}
			t.Run("change during read", func(t *testing.T) {
				fresh, err := cache.NewCacheManager(cache.ManagerConfig{}, nil)
				require.NoError(t, err)
				defer fresh.Close()
				reference := &proxy{registry: catalogDiscoveryRegistry{runtime: &catalogDiscoveryRuntime{snapshot: active}}}
				changing := &changingDiscoveryRuntime{catalogDiscoveryRuntime: &catalogDiscoveryRuntime{snapshot: active}, later: removed}
				direct := &proxy{registry: catalogDiscoveryRegistry{runtime: &catalogDiscoveryRuntime{snapshot: removed}}}
				cached := &cachedService{service: direct, runtime: changingDiscoverySource{changing}, cacheManager: fresh, cacheConfig: CacheConfig{EnableModelCache: true, EnableProviderCache: true}}
				want, err := json.Marshal(read(t, reference))
				require.NoError(t, err)
				got, err := json.Marshal(read(t, cached))
				require.NoError(t, err)
				require.JSONEq(t, string(want), string(got), "one discovery read must retain its first snapshot")
			})

		})
	}
}

type changingDiscoveryRuntime struct {
	*catalogDiscoveryRuntime
	later *runtimecatalog.RoutableSnapshot
	reads int
}

func (r *changingDiscoveryRuntime) Snapshot() *runtimecatalog.RoutableSnapshot {
	r.reads++
	if r.reads == 1 {
		return r.snapshot
	}
	return r.later
}

type changingDiscoverySource struct{ runtime connectors.RuntimeLease }

func (s changingDiscoverySource) AcquireRuntime() (connectors.RuntimeLease, error) {
	return s.runtime, nil
}
