package router

import (
	"fmt"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/routing"
)

func catalogScalingRouter(t testing.TB, count int) ModelRouter {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "author", Name: "Author"}))
	models := make(map[string]*catalogs.Model, count)
	for index := range count {
		name := fmt.Sprintf("model-%d", index)
		features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{
			Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
			Output: []catalogs.ModelModality{catalogs.ModelModalityText},
		}}
		require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{
			ID: name, Name: name, Features: features, Authors: []catalogs.Author{{ID: "author", Name: "Author"}},
		}))
		id := fmt.Sprintf("opaque-%d", index)
		models[id] = &catalogs.Model{
			ID: id, Name: name, ModelRef: catalogs.ModelDefinitionID("author/" + name),
			Status: catalogs.ModelStatusActive, Features: features,
		}
	}
	require.NoError(t, builder.SetProvider(catalogs.Provider{
		ID: "acme", Name: "Acme", Models: models,
		Inference: &catalogs.ProviderInference{
			BaseURL: "https://provider.example",
			Endpoints: []catalogs.ProviderInferenceEndpoint{{
				Operation: catalogs.ProviderOperationChatCompletions,
				Type:      catalogs.EndpointTypeOpenAI, Path: "/v1/chat/completions",
			}},
		},
	}))
	catalog, err := builder.Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(endpointBindingCatalogSource{state: starmap.CatalogState{
		Catalog: catalog, GenerationID: "scaling", Sequence: 1,
	}})
	require.NoError(t, err)
	runtimeRegistry, err := registry.Open(plane, []registry.Registration{{
		Provider: "acme", Connector: &mockConnector{name: "acme"},
		Operations:    []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtimeRegistry.Close()) })
	return New(&endpointBindingRegistry{registry: runtimeRegistry}, WithCatalog(plane))
}

func TestCatalogExactModelAllocationScaling(t *testing.T) {
	allocations := make([]float64, 0, 2)
	for _, count := range []int{10, 1000} {
		router := catalogScalingRouter(t, count)
		request := &Request{ChatRequest: &connectors.ChatRequest{Model: "author/model-0"}}
		allocations = append(allocations, testing.AllocsPerRun(20, func() {
			selected, connector, err := router.SelectModel(t.Context(), request)
			if err != nil || selected != "acme/opaque-0" || connector == nil {
				t.Fatalf("selection = %q, connector = %v, error = %v", selected, connector, err)
			}
		}))
	}
	t.Logf("exact-model allocations: 10 routes=%.0f, 1000 routes=%.0f", allocations[0], allocations[1])
	require.LessOrEqual(t, allocations[1], allocations[0]+10,
		"unrelated catalog routes must not add per-request allocations")
}

func TestCatalogIndexedSelectionMatchesFullCatalog(t *testing.T) {
	router := catalogScalingRouter(t, 10).(*modelRouter)
	runtime, err := router.registry.(connectors.LeasingRegistry).AcquireRuntime()
	require.NoError(t, err)
	defer runtime.Release()
	snapshot := runtime.Snapshot()
	for _, request := range []routing.Request{
		{Models: []string{"author/model-0"}},
		{Models: []string{"acme/opaque-0"}},
		{Models: []string{"author/model-0", "acme/opaque-0"}, AllowModelFallbacks: true},
		{Models: []string{"author/model-1", "author/model-0"}, AllowModelFallbacks: true},
		{Models: []string{"author/model-1", "author/model-0"}},
		{Models: []string{"missing"}},
		{Models: []string{"author/model-0"}, Account: routing.AccountPolicy{ModelOverrides: map[string]string{"author/model-0": "author/model-1"}}},
		{Models: []string{"author/model-0"}, Account: routing.AccountPolicy{AllowedModels: []string{"author/model-1"}}},
		{Models: []string{"missing"}, AllowAnyModelFallback: true},
		{},
	} {
		request.Operation = routing.OperationChatCompletions
		indexed, indexedErr := router.routePlanner.Plan(request, routing.Snapshot{
			CatalogGenerationID: snapshot.GenerationID(),
			Candidates:          router.toPlanningCandidates(snapshot, runtime, request),
		})
		full, fullErr := router.routePlanner.Plan(request, routing.Snapshot{
			CatalogGenerationID: snapshot.GenerationID(),
			Candidates:          router.toPlanningCandidates(snapshot, runtime, routing.Request{AllowAnyModelFallback: true}),
		})
		require.Equal(t, fmt.Sprint(fullErr), fmt.Sprint(indexedErr), "request: %+v", request)
		require.Equal(t, full, indexed, "request: %+v", request)
	}
}

func BenchmarkCatalogExactModel(b *testing.B) {
	for _, count := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprintf("routes-%d", count), func(b *testing.B) {
			router := catalogScalingRouter(b, count)
			request := &Request{ChatRequest: &connectors.ChatRequest{Model: "author/model-0"}}
			b.ReportAllocs()
			for b.Loop() {
				selected, connector, err := router.SelectModel(b.Context(), request)
				if err != nil || selected != "acme/opaque-0" || connector == nil {
					b.Fatalf("selection = %q, connector = %v, error = %v", selected, connector, err)
				}
			}
		})
	}
}
