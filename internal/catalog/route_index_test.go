package catalog

import (
	"fmt"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func routeIndexSnapshot(t testing.TB, count int) (*RoutableSnapshot, string) {
	t.Helper()
	builder := catalogs.NewEmpty()
	author := catalogs.Author{ID: "author", Name: "Author"}
	require.NoError(t, builder.SetAuthor(author))
	features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "model", Name: "Model", Authors: []catalogs.Author{author}, Features: features}))
	models := make(map[string]*catalogs.Model, count)
	last := ""
	for index := range count {
		last = fmt.Sprintf("long-provider-model-identifier-%06d", index)
		models[last] = &catalogs.Model{ID: last, Name: last, ModelRef: "author/model", Status: catalogs.ModelStatusActive, Features: features}
	}
	require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "acme", Name: "Acme", Models: models, Inference: &catalogs.ProviderInference{BaseURL: "https://provider.example", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeOpenAI, Path: "/v1/chat/completions"}}}}))
	source, err := builder.Build()
	require.NoError(t, err)
	plane, err := Open(&mutationSource{state: starmap.CatalogState{Catalog: source, GenerationID: "indexed", Sequence: 1}})
	require.NoError(t, err)
	require.NoError(t, plane.SetAdapter(AdapterAvailability{ProviderID: "acme", Registered: true, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}}))
	return plane.Current(), "acme/" + last
}

func TestExactRouteLookupAllocationsDoNotScale(t *testing.T) {
	for _, operation := range []string{"name", "route", "operation", "missing", "search-price", "model-routes"} {
		t.Run(operation, func(t *testing.T) {
			var allocations []float64
			for _, count := range []int{10, 1000} {
				snapshot, name := routeIndexSnapshot(t, count)
				allocations = append(allocations, testing.AllocsPerRun(10, func() {
					switch operation {
					case "name":
						if !snapshot.Names(name) {
							t.Fatal("missing name")
						}
					case "route":
						if route, found := snapshot.ResolveRoute(name); !found || route.ID() != name {
							t.Fatal("wrong route")
						}
					case "operation":
						if route, found := snapshot.ResolveOperation(name, catalogs.ProviderOperationChatCompletions); !found || route.ID() != name {
							t.Fatal("wrong operation route")
						}
					case "model-routes":
						routes := snapshot.RoutesForModel(name)
						if len(routes) != 1 || routes[0].ID() != name {
							t.Fatal("wrong model routes")
						}
					case "search-price":
						if _, found := snapshot.LowestSearchUnitPrice(name); found {
							t.Fatal("chat offering has no search-unit price")
						}
					case "missing":
						if snapshot.Names("absent/provider-model") {
							t.Fatal("unknown name is present")
						}
						if _, found := snapshot.ResolveRoute("absent/provider-model"); found {
							t.Fatal("unknown route is present")
						}
					}
				}))
			}
			t.Logf("allocations at 10/1000 offerings: %.0f/%.0f", allocations[0], allocations[1])
			require.LessOrEqual(t, allocations[1], allocations[0]+10)
		})
	}
}

func TestIndexedRoutesRetainOrderAndCallerOwnership(t *testing.T) {
	snapshot, name := routeIndexSnapshot(t, 3)
	want := snapshot.Routes()
	require.Equal(t, want, snapshot.RoutesForDefinition("author/model"))
	require.Equal(t, want, snapshot.RoutesForProvider("acme"))
	require.Equal(t, want, snapshot.RoutesForModel("author/model"))
	selected := snapshot.RoutesForModel(name)
	require.Len(t, selected, 1)
	before := selected[0]
	selected[0].Operations[0] = "changed"
	selected[0].Endpoints[0].URL = "https://changed.example"
	selected[0].DefinitionID = "changed/model"
	again := snapshot.RoutesForModel(name)
	require.Equal(t, before.ProviderModelID, again[0].ProviderModelID)
	require.Equal(t, want[2], again[0])
	require.True(t, snapshot.Names(name))
	require.True(t, snapshot.Names("author/model"))
	require.Empty(t, snapshot.RoutesForModel("missing/model"))
}
