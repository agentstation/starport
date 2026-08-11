package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
)

func TestSyntheticCatalogProviderInferenceContract(t *testing.T) {
	server := newSyntheticOpenAIServer(t)
	defer server.Close()

	catalog := syntheticInferenceCatalog(t, server.URL)
	plane, err := runtimecatalog.Open(syntheticCatalogSource{state: starmap.CatalogState{
		Catalog: catalog, GenerationID: "synthetic-acme", Sequence: 1,
		GeneratedAt: time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC),
	}})
	require.NoError(t, err)
	provider, err := catalog.Provider("acme")
	require.NoError(t, err)
	profile := provider.Credentials.Profiles[0]
	material := credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{"api-key": "acme-secret"},
		credentials.MaterialMetadata{Version: "synthetic"},
	)
	source := appStaticMaterialSource{material: material}

	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)
	configurations := map[catalogs.ProviderID]providers.Configuration{
		"acme": {
			Connector: connectors.ProviderConfig{
				BaseURL: server.URL, Timeout: 5 * time.Second, MaxConnections: 8, Enabled: true,
			},
			CredentialSource: source,
			Profile:          profile,
		},
	}
	registrations, err := buildRegistrations(
		plane,
		transports,
		authentication,
		configurations,
		func(
			providerID string,
			endpointTypes []catalogs.EndpointType,
			config connectors.ProviderConfig,
		) (connectors.Connector, error) {
			return transports.NewProviderConnector(catalogs.ProviderID(providerID), endpointTypes, config)
		},
	)
	require.NoError(t, err)
	require.Len(t, registrations, 1)
	require.Equal(t, "acme", registrations[0].Provider)
	require.Equal(t, []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}, registrations[0].EndpointTypes)

	runtimeRegistry, err := registry.Open(plane, registrations)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtimeRegistry.Close()) })
	connector, err := runtimeRegistry.Get("acme")
	require.NoError(t, err)
	resolved, err := runtimeRegistry.ResolveMaterial(t.Context(), "acme")
	require.NoError(t, err)

	routes := plane.Current().RoutesForProvider("acme")
	chatRoute := requireSyntheticRoute(t, routes, "opaque/chat@001", catalogs.ProviderOperationChatCompletions)
	chatEndpoint, found := chatRoute.Endpoint(catalogs.ProviderOperationChatCompletions)
	require.True(t, found)
	chatRequest := &connectors.ChatRequest{
		Model:      string(chatRoute.ProviderModelID),
		Messages:   []connectors.Message{{Role: connectors.RoleUser, Content: "hello"}},
		Endpoint:   connectors.InferenceEndpoint{Type: chatEndpoint.Type, URL: chatEndpoint.URL},
		Credential: resolved,
	}
	response, err := connector.Chat(t.Context(), chatRequest)
	require.NoError(t, err)
	require.Equal(t, "opaque/chat@001", response.Model)

	stream, err := connector.ChatStream(t.Context(), chatRequest)
	require.NoError(t, err)
	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "synthetic stream", chunk.Choices[0].Delta.Content)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, stream.Close())

	embeddingRoute := requireSyntheticRoute(t, routes, "opaque/embed@002", catalogs.ProviderOperationEmbeddings)
	embeddingEndpoint, found := embeddingRoute.Endpoint(catalogs.ProviderOperationEmbeddings)
	require.True(t, found)
	embedding, err := connector.Embeddings(t.Context(), &connectors.EmbeddingsRequest{
		Model: string(embeddingRoute.ProviderModelID), Input: "hello",
		Endpoint:   connectors.InferenceEndpoint{Type: embeddingEndpoint.Type, URL: embeddingEndpoint.URL},
		Credential: resolved,
	})
	require.NoError(t, err)
	require.Equal(t, "opaque/embed@002", embedding.Model)
	require.Equal(t, []float32{0.25, 0.75}, embedding.Data[0].Embedding)
}

type syntheticCatalogSource struct{ state starmap.CatalogState }

func (s syntheticCatalogSource) CurrentCatalogState() starmap.CatalogState { return s.state }

func syntheticInferenceCatalog(t *testing.T, baseURL string) *catalogs.Catalog {
	t.Helper()
	baselineBuilder, err := catalogs.NewEmbedded()
	require.NoError(t, err)
	baseline, err := baselineBuilder.Build()
	require.NoError(t, err)
	builder, err := catalogs.NewBuilderFrom(baseline)
	require.NoError(t, err)

	provider, err := baseline.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	chatSource := provider.Models["gpt-4o-2024-08-06"]
	require.NotNil(t, chatSource)
	chatModel := catalogs.DeepCopyModel(*chatSource)
	embeddingSource := provider.Models["text-embedding-3-small"]
	require.NotNil(t, embeddingSource)
	embeddingModel := catalogs.DeepCopyModel(*embeddingSource)
	provider.ID = "acme"
	provider.Aliases = nil
	provider.Name = "Acme Models"
	provider.Models = nil
	provider.Inference.BaseURL = baseURL
	for index := range provider.Credentials.Fields {
		provider.Credentials.Fields[index].Environment = nil
	}
	provider.Credentials.Fields[0].Environment = []string{"ACME_API_KEY"}
	require.NoError(t, builder.SetProvider(provider))

	chatModel.ID = "opaque/chat@001"
	chatModel.Name = "Acme Chat"
	require.NoError(t, builder.SetProviderModel("acme", chatModel))

	embeddingModel.ID = "opaque/embed@002"
	embeddingModel.Name = "Acme Embedding"
	embeddingModel.Features = &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{
		Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
		Output: []catalogs.ModelModality{catalogs.ModelModalityEmbedding},
	}}
	if embeddingModel.Metadata == nil {
		embeddingModel.Metadata = &catalogs.ModelMetadata{}
	}
	embeddingModel.Metadata.Tags = []catalogs.ModelTag{catalogs.ModelTagEmbedding}
	require.NoError(t, builder.SetProviderModel("acme", embeddingModel))

	result, err := builder.Build()
	require.NoError(t, err)
	return result
}

func requireSyntheticRoute(
	t *testing.T,
	routes []runtimecatalog.Route,
	providerModelID catalogs.ProviderModelID,
	operation catalogs.ProviderOperation,
) runtimecatalog.Route {
	t.Helper()
	for _, route := range routes {
		if route.ProviderModelID == providerModelID && route.Supports(operation) {
			return route
		}
	}
	t.Fatalf("synthetic route %s with %s is missing: %#v", providerModelID, operation, routes)
	return runtimecatalog.Route{}
}

func newSyntheticOpenAIServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got, want := request.Header.Get("Authorization"), "Bearer acme-secret"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
			http.Error(writer, "unexpected authorization", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/v1/chat/completions":
			var body connectors.ChatRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode chat request: %v", err)
				http.Error(writer, "invalid chat request", http.StatusBadRequest)
				return
			}
			if body.Stream {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(writer, "data: {\"id\":\"chunk\",\"object\":\"chat.completion.chunk\",\"model\":\"opaque/chat@001\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"synthetic stream\"}}]}\n\ndata: [DONE]\n\n")
				return
			}
			_ = json.NewEncoder(writer).Encode(connectors.ChatResponse{
				ID: "response", Object: "chat.completion", Model: body.Model,
				Choices: []connectors.Choice{{
					Index: 0, Message: connectors.Message{Role: connectors.RoleAssistant, Content: "synthetic chat"},
				}},
			})
		case "/v1/embeddings":
			var body connectors.EmbeddingsRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode embeddings request: %v", err)
				http.Error(writer, "invalid embeddings request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(connectors.EmbeddingsResponse{
				Object: "list", Model: body.Model,
				Data: []connectors.Embedding{{Object: "embedding", Index: 0, Embedding: []float32{0.25, 0.75}}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
}
