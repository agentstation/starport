package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestStarmapFactMutationContract(t *testing.T) {
	firstCatalog := mutationCatalog(t, mutationFacts{
		definitionName: "First definition",
		operation:      catalogs.ProviderOperationChatCompletions,
		protocol:       catalogs.EndpointTypeOpenAI,
		baseURL:        "https://first.provider.test",
		path:           "/models/{provider_model_id}:chat",
		inputPrice:     1.25,
		promptCache:    true,
	})
	source := &mutationSource{state: starmap.CatalogState{
		Catalog: firstCatalog, GenerationID: "generation-1", Sequence: 1,
		GeneratedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
	}}
	plane, err := Open(source)
	require.NoError(t, err)
	require.NoError(t, plane.SetAdapter(AdapterAvailability{
		ProviderID: "provider", Registered: true, Configured: true,
		Operations: []catalogs.ProviderOperation{
			catalogs.ProviderOperationChatCompletions,
			catalogs.ProviderOperationEmbeddings,
		},
		EndpointTypes: []catalogs.EndpointType{
			catalogs.EndpointTypeOpenAI,
			catalogs.EndpointTypeGoogle,
		},
	}))

	const routeID = "provider/opaque/model@001"
	first := plane.Current()
	firstRoute, found := first.ResolveRoute(routeID)
	require.True(t, found)
	require.Equal(t, catalogs.ProviderModelID("opaque/model@001"), firstRoute.ProviderModelID)
	require.True(t, firstRoute.Supports(catalogs.ProviderOperationChatCompletions))
	require.False(t, firstRoute.Supports(catalogs.ProviderOperationEmbeddings))
	require.True(t, firstRoute.SupportsPromptCache())
	firstEndpoint, found := firstRoute.Endpoint(catalogs.ProviderOperationChatCompletions)
	require.True(t, found)
	require.Equal(t, catalogs.EndpointTypeOpenAI, firstEndpoint.Type)
	require.Equal(t, "https://first.provider.test/models/opaque/model@001:chat", firstEndpoint.URL)
	firstOffering, err := first.Offering(firstRoute)
	require.NoError(t, err)
	require.Equal(t, 1.25, firstOffering.Pricing.Tokens.Input.Per1M)

	secondCatalog := mutationCatalog(t, mutationFacts{
		definitionName: "Second definition",
		operation:      catalogs.ProviderOperationEmbeddings,
		protocol:       catalogs.EndpointTypeGoogle,
		baseURL:        "https://second.provider.test",
		path:           "/models/{provider_model_id}:embedContent",
		inputPrice:     2.5,
	})
	source.state = starmap.CatalogState{
		Catalog: secondCatalog, GenerationID: "generation-2", Sequence: 2,
		GeneratedAt: time.Date(2026, 8, 3, 12, 1, 0, 0, time.UTC),
	}
	require.NoError(t, plane.Refresh())

	second := plane.Current()
	secondRoute, found := second.ResolveRoute(routeID)
	require.True(t, found)
	require.Equal(t, catalogs.ProviderModelID("opaque/model@001"), secondRoute.ProviderModelID)
	require.False(t, secondRoute.Supports(catalogs.ProviderOperationChatCompletions))
	require.True(t, secondRoute.Supports(catalogs.ProviderOperationEmbeddings))
	require.False(t, secondRoute.SupportsPromptCache())
	secondEndpoint, found := secondRoute.Endpoint(catalogs.ProviderOperationEmbeddings)
	require.True(t, found)
	require.Equal(t, catalogs.EndpointTypeGoogle, secondEndpoint.Type)
	require.Equal(t, "https://second.provider.test/models/opaque/model@001:embedContent", secondEndpoint.URL)
	definition, err := second.Definition(secondRoute.DefinitionID)
	require.NoError(t, err)
	require.Equal(t, "Second definition", definition.Name)
	secondOffering, err := second.Offering(secondRoute)
	require.NoError(t, err)
	require.Equal(t, 2.5, secondOffering.Pricing.Tokens.Input.Per1M)

	// A retained snapshot stays generation-consistent after publication.
	require.Equal(t, "generation-1", first.GenerationID())
	require.Equal(t, "generation-2", second.GenerationID())
}

func TestEndpointBindingsAndStreamURLComeFromStarmap(t *testing.T) {
	catalog := mutationCatalog(t, mutationFacts{
		definitionName: "Bound definition",
		operation:      catalogs.ProviderOperationChatCompletions,
		protocol:       catalogs.EndpointTypeGoogleCloud,
		baseURL:        "https://{location}.provider.test",
		path:           "/projects/{project}/models/{provider_model_id}:invoke",
		streamPath:     "/projects/{project}/models/{provider_model_id}:streamInvoke",
		inputPrice:     1,
	})
	plane, err := Open(&mutationSource{state: starmap.CatalogState{
		Catalog: catalog, GenerationID: "generation-bound", Sequence: 1,
		GeneratedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
	}})
	require.NoError(t, err)
	require.NoError(t, plane.SetAdapter(AdapterAvailability{
		ProviderID: "provider", Registered: true, Configured: true,
		Operations:    []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeGoogleCloud},
		BaseURL:       "https://private.provider.test",
		EndpointBindings: map[string]string{
			"location": "us-test1",
			"project":  "tenant-project",
		},
	}))
	route, found := plane.Current().ResolveRoute("provider/opaque/model@001")
	require.True(t, found)
	endpoint, found := route.Endpoint(catalogs.ProviderOperationChatCompletions)
	require.True(t, found)
	require.Equal(t, "https://private.provider.test/projects/tenant-project/models/opaque/model@001:invoke", endpoint.URL)
	require.Equal(t, "https://private.provider.test/projects/tenant-project/models/opaque/model@001:streamInvoke", endpoint.StreamURL)
}

func TestUnbindableOfferingOperationIsNotRoutable(t *testing.T) {
	catalog := mutationCatalog(t, mutationFacts{
		definitionName: "Bound definition",
		operation:      catalogs.ProviderOperationChatCompletions,
		protocol:       catalogs.EndpointTypeGoogleCloud,
		baseURL:        "https://{location}.provider.test",
		path:           "/projects/{project}/models/{provider_model_id}:invoke",
	})
	plane, err := Open(&mutationSource{state: starmap.CatalogState{
		Catalog: catalog, GenerationID: "generation-unbound", Sequence: 1,
		GeneratedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
	}})
	require.NoError(t, err)
	require.NoError(t, plane.SetAdapter(AdapterAvailability{
		ProviderID: "provider", Registered: true, Configured: true,
		Operations:    []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeGoogleCloud},
		EndpointBindings: map[string]string{
			"location": "us-test1",
		},
	}))
	require.Empty(t, plane.Current().Routes())
}

type mutationSource struct {
	state starmap.CatalogState
}

func (s *mutationSource) CurrentCatalogState() starmap.CatalogState {
	return s.state
}

type mutationFacts struct {
	definitionName string
	operation      catalogs.ProviderOperation
	protocol       catalogs.EndpointType
	baseURL        string
	path           string
	streamPath     string
	inputPrice     float64
	promptCache    bool
}

func mutationCatalog(t *testing.T, facts mutationFacts) *catalogs.Catalog {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "author", Name: "Author"}))
	modalities := catalogs.ModelModalities{
		Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
		Output: []catalogs.ModelModality{catalogs.ModelModalityText},
	}
	metadata := &catalogs.ModelMetadata{}
	if facts.operation == catalogs.ProviderOperationEmbeddings {
		modalities.Output = []catalogs.ModelModality{catalogs.ModelModalityEmbedding}
		metadata.Tags = []catalogs.ModelTag{catalogs.ModelTagEmbedding}
	}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{
		ID: "model", Name: facts.definitionName,
		Authors:  []catalogs.Author{{ID: "author", Name: "Author"}},
		Metadata: metadata,
		Features: &catalogs.ModelFeatures{Modalities: modalities},
	}))
	pricing := &catalogs.ModelPricing{
		Currency: catalogs.ModelPricingCurrencyUSD,
		Tokens: &catalogs.ModelTokenPricing{
			Input:  &catalogs.ModelTokenCost{Per1M: facts.inputPrice},
			Output: &catalogs.ModelTokenCost{Per1M: facts.inputPrice * 2},
		},
	}
	if facts.promptCache {
		pricing.Tokens.CacheRead = &catalogs.ModelTokenCost{Per1M: 0.1}
		pricing.Tokens.CacheWrite = &catalogs.ModelTokenCost{Per1M: 0.2}
	}
	require.NoError(t, builder.SetProvider(catalogs.Provider{
		ID: "provider", Name: "Provider",
		Inference: &catalogs.ProviderInference{
			BaseURL: facts.baseURL,
			Endpoints: []catalogs.ProviderInferenceEndpoint{{
				Operation: facts.operation, Type: facts.protocol, Path: facts.path, StreamPath: facts.streamPath,
			}},
		},
		Models: map[string]*catalogs.Model{"opaque/model@001": {
			ID: "opaque/model@001", ModelRef: "author/model", Name: "Provider model",
			Status: catalogs.ModelStatusActive, Metadata: metadata,
			Features: &catalogs.ModelFeatures{Modalities: modalities}, Pricing: pricing,
		}},
	}))
	catalog, err := builder.Build()
	require.NoError(t, err)
	return catalog
}
