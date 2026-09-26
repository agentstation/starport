package proxy

import (
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestCacheDisabledKindsBypassLookups(t *testing.T) {
	for _, kind := range []string{"chat", "stream", "embeddings", "models", "providers", "endpoints"} {
		t.Run(kind, func(t *testing.T) {
			manager := newMockCacheManager()
			manager.shouldError = true
			upstream := &mockProxyImpl{chatResponse: canonicalChatResponse(), modelsResponse: &ModelsResponse{}, providersResponse: &ProvidersResponse{}, embeddingsResponse: &EmbeddingsResponse{}}
			service := &cachedService{service: upstream, cacheManager: manager}
			switch kind {
			case "chat":
				_, err := service.ProcessChatCompletion(t.Context(), testChatRequest("account"))
				require.NoError(t, err)
			case "stream":
				stream, err := service.ProcessChatCompletionStream(t.Context(), testChatRequest("account"))
				require.NoError(t, err)
				readAllEvents(t, stream)
				require.NoError(t, stream.Close())
			case "embeddings":
				_, err := service.ProcessEmbeddings(t.Context(), &EmbeddingsRequest{AccountID: "account", Request: inference.EmbeddingRequest{Model: "model"}})
				require.NoError(t, err)
			case "models":
				_, err := service.ListModels(t.Context())
				require.NoError(t, err)
			case "providers":
				_, err := service.ListProviders(t.Context())
				require.NoError(t, err)
			case "endpoints":
				_, err := service.GetModelEndpoints(t.Context(), "model")
				require.NoError(t, err)
			}
			require.Empty(t, manager.calls, "disabled kind must perform neither cache reads nor fills")
		})
	}
}
