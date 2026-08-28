package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/failure"
)

// rerankTestDocuments is the list every test below ranks. The answer each fake
// provider returns names positions in it.
var rerankTestDocuments = []string{
	"Cohere serves reranking.",
	"A poem about the sea.",
	"Voyage AI serves reranking.",
}

// TestBothRerankProtocolsSpeakOneRequest is the test the operation turns on.
// Cohere names the result count top_n and answers under results; Voyage AI
// names it top_k and answers under data. One canonical request produces both
// bodies, and both answers produce the same canonical response, so nothing
// above the transport learns which provider ran.
func TestBothRerankProtocolsSpeakOneRequest(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name            string
		providerID      catalogs.ProviderID
		endpointType    catalogs.EndpointType
		answer          string
		wantCountField  string
		otherCountField string
		wantSearchUnits int
		wantTotalTokens int
	}{
		{
			name:         "cohere",
			providerID:   "cohere",
			endpointType: catalogs.EndpointTypeCohere,
			answer: `{"results":[{"index":2,"relevance_score":0.91},` +
				`{"index":0,"relevance_score":0.42}],` +
				`"meta":{"billed_units":{"search_units":1},` +
				`"tokens":{"input_tokens":38,"output_tokens":0}}}`,
			wantCountField:  "top_n",
			otherCountField: "top_k",
			wantSearchUnits: 1,
			wantTotalTokens: 38,
		},
		{
			name:         "voyage",
			providerID:   "voyage",
			endpointType: catalogs.EndpointTypeVoyage,
			answer: `{"object":"list","data":[{"index":2,"relevance_score":0.91},` +
				`{"index":0,"relevance_score":0.42}],` +
				`"model":"rerank-2.5","usage":{"total_tokens":38}}`,
			wantCountField:  "top_k",
			otherCountField: "top_n",
			wantSearchUnits: 0,
			wantTotalTokens: 38,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var sent map[string]any
			var authorization string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authorization = r.Header.Get("Authorization")
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(body, &sent))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(testCase.answer))
			}))
			defer server.Close()

			reranker := productionReranker(t, testCase.providerID, testCase.endpointType, server.URL)
			topN := 2
			response, err := reranker.Rerank(context.Background(), &RerankRequest{
				MediaTarget: MediaTarget{
					Model:      "rerank-model",
					Endpoint:   InferenceEndpoint{Type: testCase.endpointType, URL: server.URL},
					Credential: testAPIMaterial("rerank-key"),
				},
				Query:     "who ships reranking",
				Documents: rerankTestDocuments,
				TopN:      &topN,
			})
			require.NoError(t, err)

			// The provider read its own wire words and none of the other
			// provider's.
			require.Equal(t, float64(2), sent[testCase.wantCountField])
			require.NotContains(t, sent, testCase.otherCountField)
			require.Equal(t, "who ships reranking", sent["query"])
			require.Equal(t, "rerank-model", sent["model"])
			// The credential is placed through the provider auth registry
			// rather than written by the transport.
			require.Equal(t, "Bearer rerank-key", authorization)

			// Both answers reduce to the same canonical ranking.
			require.Equal(t, []RerankResult{
				{Index: 2, RelevanceScore: 0.91},
				{Index: 0, RelevanceScore: 0.42},
			}, response.Results)
			require.Equal(t, testCase.wantSearchUnits, response.SearchUnits)
			require.NotNil(t, response.Usage)
			require.Equal(t, testCase.wantTotalTokens, response.Usage.TotalTokens)
		})
	}
}

// TestVoyageRefusesATokenCapItCannotExpress holds the rule that separates a
// translation from a lie. Voyage AI has no per-document token cap: it switches
// truncation on or off. Sending the request without the cap would read as
// success while the caller paid for every whole document it asked to bound.
func TestVoyageRefusesATokenCapItCannotExpress(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a request the provider cannot express reached the provider")
	}))
	defer server.Close()

	reranker := productionReranker(t, "voyage", catalogs.EndpointTypeVoyage, server.URL)
	tokenCap := 4096
	_, err := reranker.Rerank(context.Background(), &RerankRequest{
		MediaTarget: MediaTarget{
			Model:      "rerank-2.5",
			Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeVoyage, URL: server.URL},
			Credential: testAPIMaterial("rerank-key"),
		},
		Query:                "who ships reranking",
		Documents:            rerankTestDocuments,
		MaxTokensPerDocument: &tokenCap,
	})
	require.ErrorIs(t, err, ErrRerankOptionUnsupported)
}

// TestARerankResultOutsideTheRequestIsRefused stops a provider defect from
// becoming a wrong answer. A result names a document by position, so a
// position the request never held resolves to the wrong document or to none.
func TestARerankResultOutsideTheRequestIsRefused(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":7,"relevance_score":0.9}]}`))
	}))
	defer server.Close()

	reranker := productionReranker(t, "cohere", catalogs.EndpointTypeCohere, server.URL)
	_, err := reranker.Rerank(context.Background(), &RerankRequest{
		MediaTarget: MediaTarget{
			Model:      "rerank-v3.5",
			Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeCohere, URL: server.URL},
			Credential: testAPIMaterial("rerank-key"),
		},
		Query:     "who ships reranking",
		Documents: rerankTestDocuments,
	})
	require.ErrorContains(t, err, "rerank result 7 for 3 documents")
}

// TestARerankRejectionNormalizesLikeAChatRejection holds the second half of
// RNK-V07. Reranking adds no failure class: a rerank rejection reaches the
// caller through the same normalization the chat path uses, and the provider's
// own words stay in the provider details rather than in the safe message.
func TestARerankRejectionNormalizesLikeAChatRejection(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		providerID catalogs.ProviderID
		endpoint   catalogs.EndpointType
		status     int
		body       string
		wantKind   failure.Kind
		wantScope  failure.StateScope
		retryable  bool
		wantDetail string
	}{
		{
			name: "cohere states the reason under message", providerID: "cohere",
			endpoint: catalogs.EndpointTypeCohere, status: http.StatusUnauthorized,
			body: `{"message":"invalid api token"}`, wantKind: failure.Authentication,
			wantScope: failure.ScopeCredential, wantDetail: "invalid api token",
		},
		{
			name: "voyage states the reason under detail", providerID: "voyage",
			endpoint: catalogs.EndpointTypeVoyage, status: http.StatusTooManyRequests,
			body: `{"detail":"rate limit exceeded"}`, wantKind: failure.RateLimit,
			wantScope: failure.ScopeOffering, retryable: true, wantDetail: "rate limit exceeded",
		},
		{
			name: "too many documents is a validation failure", providerID: "cohere",
			endpoint: catalogs.EndpointTypeCohere, status: http.StatusBadRequest,
			body: `{"message":"too many documents"}`, wantKind: failure.Validation,
			wantScope: failure.ScopeNone, wantDetail: "too many documents",
		},
		{
			name: "an unreadable rejection still carries its status", providerID: "voyage",
			endpoint: catalogs.EndpointTypeVoyage, status: http.StatusBadGateway,
			body: `upstream connect error`, wantKind: failure.ProviderUnavailable,
			wantScope: failure.ScopeOffering, retryable: true, wantDetail: "upstream connect error",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()

			reranker := productionReranker(t, testCase.providerID, testCase.endpoint, server.URL)
			_, err := reranker.Rerank(context.Background(), &RerankRequest{
				MediaTarget: MediaTarget{
					Model:      "rerank-model",
					Endpoint:   InferenceEndpoint{Type: testCase.endpoint, URL: server.URL},
					Credential: testAPIMaterial("rerank-key"),
				},
				Query:     "who ships reranking",
				Documents: rerankTestDocuments,
			})
			require.Error(t, err)

			normalized := NormalizeFailure(string(testCase.providerID), err)
			require.Equal(t, testCase.wantKind, normalized.Kind())
			require.Equal(t, testCase.retryable, normalized.Retryable())
			require.Equal(t, testCase.wantScope, normalized.StateScope())
			require.Equal(t, testCase.wantDetail, normalized.ProviderDetails().Message)
			// The provider's own words stay out of the client-safe message.
			require.NotContains(t, normalized.SafeMessage(), testCase.wantDetail)
		})
	}
}

// TestTheRegistryAcceptsARerankDescriptorAndRefusesAnUnknownOperation holds
// RNK-V06. The descriptor guard reads the planner's operation set, so an
// operation the transports declare and the planner cannot route would compile
// a transport no request reaches.
func TestTheRegistryAcceptsARerankDescriptorAndRefusesAnUnknownOperation(t *testing.T) {
	t.Parallel()

	registry, err := NewTransportRegistry(TransportDescriptor{
		EndpointType: catalogs.EndpointTypeCohere,
		Operations:   []catalogs.ProviderOperation{catalogs.ProviderOperationRerank},
		Factory:      newCohereConnector,
	})
	require.NoError(t, err)
	require.True(t, registry.Supports(catalogs.EndpointTypeCohere, catalogs.ProviderOperationRerank))

	_, err = NewTransportRegistry(TransportDescriptor{
		EndpointType: catalogs.EndpointTypeCohere,
		Operations:   []catalogs.ProviderOperation{"reranking"},
		Factory:      newCohereConnector,
	})
	require.ErrorContains(t, err, `operation "reranking" is invalid`)
	require.ErrorContains(t, err, string(catalogs.ProviderOperationRerank))
}

// TestADescriptorCannotDeclareRerankWithoutTheInterface extends the activation
// guard to the new operation. A descriptor that named rerank over a transport
// that cannot perform it would fail once per request instead of once at
// startup.
func TestADescriptorCannotDeclareRerankWithoutTheInterface(t *testing.T) {
	t.Parallel()

	registry, err := NewTransportRegistry(TransportDescriptor{
		EndpointType: catalogs.EndpointTypeOpenAI,
		Operations: []catalogs.ProviderOperation{
			catalogs.ProviderOperationChatCompletions,
			catalogs.ProviderOperationRerank,
		},
		Factory: func(catalogs.ProviderID, ProviderConfig) (Connector, error) {
			return NewMockConnector(ProviderConfig{}), nil
		},
	})
	require.NoError(t, err)

	_, err = registry.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		mediaTestConfig("https://provider.example"),
	)
	require.ErrorIs(t, err, ErrTransportInterfaceMissing)
	require.Contains(t, err.Error(), "Reranker")
}

// TestAConnectorWithoutTheRerankOperationRefuses is the request-time half of
// the same rule, and it is what a router reads before it spends a credential.
// The probe reads the transport the route selected rather than the composed
// connector, because one provider connector can span protocols.
func TestAConnectorWithoutTheRerankOperationRefuses(t *testing.T) {
	t.Parallel()

	registry, err := ProductionTransportRegistry()
	require.NoError(t, err)
	connector, err := registry.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI, catalogs.EndpointTypeCohere},
		mediaTestConfig("https://provider.example"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connector.Close() })

	_, cohereServes := RerankerFor(connector, catalogs.EndpointTypeCohere)
	require.True(t, cohereServes)
	_, openAIServes := RerankerFor(connector, catalogs.EndpointTypeOpenAI)
	require.False(t, openAIServes)

	// Reranking stays off Connector for the reason the media interfaces stay
	// off it: five of the seven compiled transports serve none of it, and a
	// method they all answered would stop the compiler reporting which ones
	// actually can.
	require.Equal(t, 1, reflect.TypeOf((*Reranker)(nil)).Elem().NumMethod())
}

// TestARerankTransportRefusesTheOperationsItDoesNotServe holds the other
// direction. Both rerank providers publish routes this gateway does not
// compile, and the transport answers those calls with a named refusal rather
// than a request that cannot succeed.
func TestARerankTransportRefusesTheOperationsItDoesNotServe(t *testing.T) {
	t.Parallel()

	connector, err := newCohereConnector("cohere", mediaTestConfig("https://provider.example"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connector.Close() })

	_, chatErr := connector.Chat(context.Background(), &ChatRequest{})
	require.ErrorIs(t, chatErr, ErrTransportOperationUnsupported)
	_, streamErr := connector.ChatStream(context.Background(), &ChatRequest{})
	require.ErrorIs(t, streamErr, ErrTransportOperationUnsupported)
	_, embeddingsErr := connector.Embeddings(context.Background(), &EmbeddingsRequest{})
	require.ErrorIs(t, embeddingsErr, ErrTransportOperationUnsupported)
}

// productionReranker composes one provider through the shipped registry, so a
// test exercises the descriptor the gateway actually registers rather than a
// transport built beside it.
func productionReranker(
	t *testing.T,
	providerID catalogs.ProviderID,
	endpointType catalogs.EndpointType,
	baseURL string,
) Reranker {
	t.Helper()
	registry, err := ProductionTransportRegistry()
	require.NoError(t, err)
	connector, err := registry.NewProviderConnector(
		providerID,
		[]catalogs.EndpointType{endpointType},
		mediaTestConfig(baseURL),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connector.Close() })
	reranker, implemented := RerankerFor(connector, endpointType)
	require.True(t, implemented)
	return reranker
}
