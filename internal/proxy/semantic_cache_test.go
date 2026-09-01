package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

// fakeEmbedder answers a fixed vector per prompt text, so tests choose
// exactly how similar two prompts look to the index.
type fakeEmbedder struct {
	vectors    map[string][]float32
	calls      int
	identities []SemanticEmbedIdentity
	err        error
}

func (f *fakeEmbedder) Embed(_ context.Context, identity SemanticEmbedIdentity, text string) ([]float32, error) {
	f.calls++
	f.identities = append(f.identities, identity)
	if f.err != nil {
		return nil, f.err
	}
	return f.vectors[text], nil
}

const (
	semanticPromptText     = "What is the capital of France?"
	semanticParaphraseText = "Tell me the capital city of France."
	semanticUnrelatedText  = "Describe the tides of the North Sea."
)

func newFakeEmbedder() *fakeEmbedder {
	return &fakeEmbedder{vectors: map[string][]float32{
		// The prompt and its paraphrase sit at cosine similarity 0.99.
		// The unrelated prompt sits orthogonal to both.
		"user: " + semanticPromptText:     {1, 0, 0},
		"user: " + semanticParaphraseText: {0.99, 0.14, 0},
		"user: " + semanticUnrelatedText:  {0, 1, 0},
	}}
}

func semanticChatRequest(accountID, text string) *ChatCompletionRequest {
	request := testChatRequest(accountID)
	request.Request.Messages[0].Content[0].Text = text
	request.SemanticCache = true
	request.RequestID = "req-1"
	return request
}

func semanticCachedService(upstream Proxy, manager CacheManager, embedder SemanticEmbedder) *cachedService {
	return &cachedService{
		service: upstream, generation: "catalog-1", cacheManager: manager,
		cacheConfig: CacheConfig{
			EnableChatCache:     true,
			EnableSemanticCache: true,
			SemanticEmbedder:    embedder,
		},
	}
}

func TestSemanticCacheAnswersAParaphrase(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	embedder := newFakeEmbedder()
	service := semanticCachedService(upstream, cacheManager, embedder)

	first, err := service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticPromptText))
	require.NoError(t, err)
	require.Equal(t, CacheStatusMiss, first.CacheStatus)

	second, err := service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticParaphraseText))
	require.NoError(t, err)
	require.Equal(t, CacheStatusHit, second.CacheStatus)
	require.Greater(t, second.CacheSimilarity, 0.95)
	require.Equal(t, "chatcmpl-test", second.Response.ID)
	require.Equal(t, 1, upstream.calls["ProcessChatCompletion"])

	// The embedding ran under the calling request's own identity.
	require.NotEmpty(t, embedder.identities)
	require.Equal(t, "account-1", embedder.identities[0].AccountID)
	require.Equal(t, "req-1", embedder.identities[0].RequestID)
}

func TestSemanticCacheRefusesBelowThreshold(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	service := semanticCachedService(upstream, cacheManager, newFakeEmbedder())

	_, err := service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticPromptText))
	require.NoError(t, err)

	response, err := service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticUnrelatedText))
	require.NoError(t, err)
	require.Equal(t, CacheStatusMiss, response.CacheStatus)
	require.Equal(t, 2, upstream.calls["ProcessChatCompletion"])
}

func TestSemanticCacheNeedsDeploymentOptIn(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	embedder := newFakeEmbedder()
	service := semanticCachedService(upstream, cacheManager, embedder)
	service.cacheConfig.EnableSemanticCache = false

	_, err := service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticPromptText))
	require.NoError(t, err)
	_, err = service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticParaphraseText))
	require.NoError(t, err)

	require.Equal(t, 0, embedder.calls)
	require.Equal(t, 2, upstream.calls["ProcessChatCompletion"])
}

func TestSemanticCacheNeedsRequestOptIn(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	embedder := newFakeEmbedder()
	service := semanticCachedService(upstream, cacheManager, embedder)

	first := semanticChatRequest("account-1", semanticPromptText)
	first.SemanticCache = false
	second := semanticChatRequest("account-1", semanticParaphraseText)
	second.SemanticCache = false

	_, err := service.ProcessChatCompletion(context.Background(), first)
	require.NoError(t, err)
	_, err = service.ProcessChatCompletion(context.Background(), second)
	require.NoError(t, err)

	require.Equal(t, 0, embedder.calls)
	require.Equal(t, 2, upstream.calls["ProcessChatCompletion"])
}

func TestSemanticCacheFailsOpenOnEmbedError(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	embedder := &fakeEmbedder{err: errors.New("embedding provider unavailable")}
	service := semanticCachedService(upstream, cacheManager, embedder)

	response, err := service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticPromptText))
	require.NoError(t, err)
	require.Equal(t, CacheStatusMiss, response.CacheStatus)

	response, err = service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticParaphraseText))
	require.NoError(t, err)
	require.Equal(t, CacheStatusMiss, response.CacheStatus)
	require.Equal(t, 2, upstream.calls["ProcessChatCompletion"])
}

func TestSemanticCacheDropsAVectorWhoseEntryLeft(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	service := semanticCachedService(upstream, cacheManager, newFakeEmbedder())

	_, err := service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticPromptText))
	require.NoError(t, err)

	// Evict the exact entry the stored vector points at.
	var staleKey string
	for key := range cacheManager.storage {
		if strings.Contains(key, ":chat:") {
			staleKey = key
			delete(cacheManager.storage, key)
		}
	}
	require.NotEmpty(t, staleKey)

	// The paraphrase matches the vector, finds no entry behind it, and
	// pays the provider instead of answering a ghost.
	response, err := service.ProcessChatCompletion(context.Background(), semanticChatRequest("account-1", semanticParaphraseText))
	require.NoError(t, err)
	require.Equal(t, CacheStatusMiss, response.CacheStatus)
	require.Equal(t, 2, upstream.calls["ProcessChatCompletion"])

	// The stale vector left the index with its entry.
	for key, data := range cacheManager.storage {
		if !strings.Contains(key, "semantic_cache") {
			continue
		}
		var record struct {
			Entries []struct {
				Key string `json:"key"`
			} `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(data, &record))
		for _, entry := range record.Entries {
			require.NotEqual(t, staleKey, entry.Key)
		}
	}
}

func TestSemanticCacheStreamAnswersAParaphrase(t *testing.T) {
	cacheManager := newMockCacheManager()
	upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
	service := semanticCachedService(upstream, cacheManager, newFakeEmbedder())

	first := semanticChatRequest("account-1", semanticPromptText)
	first.Request.Stream = true
	stream, err := service.ProcessChatCompletionStream(context.Background(), first)
	require.NoError(t, err)
	readAllEvents(t, stream)

	second := semanticChatRequest("account-1", semanticParaphraseText)
	second.Request.Stream = true
	replay, err := service.ProcessChatCompletionStream(context.Background(), second)
	require.NoError(t, err)
	events := readAllEvents(t, replay)
	require.NotEmpty(t, events)

	provider, ok := replay.(CacheSimilarityProvider)
	require.True(t, ok)
	require.Greater(t, provider.GetCacheSimilarity(), 0.95)
	require.Equal(t, 1, upstream.calls["ProcessChatCompletionStream"])
}

type embeddingCaptureProxy struct {
	mockProxyImpl
	request  *EmbeddingsRequest
	response *EmbeddingsResponse
}

func (p *embeddingCaptureProxy) ProcessEmbeddings(_ context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	p.request = req
	return p.response, nil
}

func TestGatewayEmbedderRidesTheGatewayEmbeddingsPath(t *testing.T) {
	gateway := &embeddingCaptureProxy{response: &EmbeddingsResponse{
		Response: inference.EmbeddingResponse{Data: []inference.Embedding{{Index: 0, Vector: []float32{1, 0}}}},
	}}
	embedder := NewGatewayEmbedder("openai/text-embedding-3-small", func() Proxy { return gateway })

	vector, err := embedder.Embed(context.Background(), SemanticEmbedIdentity{
		AccountID: "account-1", KeyID: "key-1", TeamID: "team-1",
		RequestID: "req-1", Protocol: "openai",
	}, "prompt text")
	require.NoError(t, err)
	require.Equal(t, []float32{1, 0}, vector)

	require.NotNil(t, gateway.request)
	require.Equal(t, "openai/text-embedding-3-small", gateway.request.Request.Model)
	require.Equal(t, []string{"prompt text"}, gateway.request.Request.Input.Texts)
	require.Equal(t, "account-1", gateway.request.AccountID)
	require.Equal(t, "req-1-semantic-cache", gateway.request.RequestID)
	require.Equal(t, "openai", gateway.request.Protocol)
}

func TestGatewayEmbedderRefusesAWrongVectorCount(t *testing.T) {
	gateway := &embeddingCaptureProxy{response: &EmbeddingsResponse{}}
	embedder := NewGatewayEmbedder("openai/text-embedding-3-small", func() Proxy { return gateway })

	_, err := embedder.Embed(context.Background(), SemanticEmbedIdentity{RequestID: "req-1"}, "prompt text")
	require.Error(t, err)
}
