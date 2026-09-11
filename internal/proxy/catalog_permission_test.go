package proxy

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

type cachePermissionSource struct {
	*starmap.Client
	allowed atomic.Bool
}

func (s *cachePermissionSource) AllowsCatalogAttempt(catalogs.CatalogAuthorityHead) bool {
	return s.allowed.Load()
}

type withdrawingCacheManager struct {
	*mockCacheManager
	onRead func()
}

func (m *withdrawingCacheManager) GetResponse(ctx context.Context, key string) ([]byte, bool, error) {
	data, found, err := m.mockCacheManager.GetResponse(ctx, key)
	if m.onRead != nil {
		m.onRead()
	}
	return data, found, err
}

func TestCacheDeliveryRechecksCatalogPermission(t *testing.T) {
	for _, mode := range []string{"chat lookup", "stream lookup", "stream first read", "admitted stream"} {
		t.Run(mode, func(t *testing.T) {
			client, err := starmap.New()
			require.NoError(t, err)
			source := &cachePermissionSource{Client: client}
			source.allowed.Store(true)
			plane, err := runtimecatalog.Open(source)
			require.NoError(t, err)
			manager := &withdrawingCacheManager{mockCacheManager: newMockCacheManager()}
			upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
			service := &cachedService{
				service: upstream, runtime: &cacheRuntimeSource{snapshot: plane.Current()}, cacheManager: manager,
				cacheConfig: CacheConfig{EnableChatCache: true},
			}
			request := testChatRequest("account-1")
			_, err = service.ProcessChatCompletion(t.Context(), request)
			require.NoError(t, err)
			if mode == "chat lookup" || mode == "stream lookup" {
				manager.onRead = func() { source.allowed.Store(false) }
			}
			if mode == "chat lookup" {
				response, err := service.ProcessChatCompletion(t.Context(), request)
				require.Nil(t, response)
				requireCatalogPermissionRefusal(t, err)
			} else {
				request.Request.Stream = true
				stream, err := service.ProcessChatCompletionStream(t.Context(), request)
				if mode == "stream lookup" {
					if stream != nil {
						require.NoError(t, stream.Close())
					}
					require.Nil(t, stream)
					requireCatalogPermissionRefusal(t, err)
				} else {
					require.NoError(t, err)
					defer stream.Close()
					if mode == "admitted stream" {
						_, err = stream.Read()
						require.NoError(t, err)
					}
					source.allowed.Store(false)
					if mode == "stream first read" {
						event, err := stream.Read()
						require.Nil(t, event)
						requireCatalogPermissionRefusal(t, err)
						source.allowed.Store(true)
						event, err = stream.Read()
						require.Nil(t, event)
						requireCatalogPermissionRefusal(t, err)
					} else {
						readAllEvents(t, stream)
					}
				}
			}
			require.Equal(t, 1, upstream.calls["ProcessChatCompletion"])
		})
	}
}

func requireCatalogPermissionRefusal(t *testing.T, err error) {
	t.Helper()
	var refusal *failure.Failure
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, failure.Kind("gateway_unavailable"), refusal.Kind())
	require.True(t, refusal.Retryable())
	require.Equal(t, failure.ScopeNone, refusal.ProviderDetails().StateScope)
}

func TestEmbeddingAndSemanticCachePermission(t *testing.T) {
	for _, mode := range []string{"embedding", "semantic chat", "semantic stream"} {
		t.Run(mode, func(t *testing.T) {
			client, err := starmap.New()
			require.NoError(t, err)
			source := &cachePermissionSource{Client: client}
			source.allowed.Store(true)
			plane, err := runtimecatalog.Open(source)
			require.NoError(t, err)
			manager := &withdrawingCacheManager{mockCacheManager: newMockCacheManager()}
			upstream := &mockProxyImpl{chatResponse: canonicalChatResponse(), embeddingsResponse: &EmbeddingsResponse{
				Response: inference.EmbeddingResponse{Model: "openai/embed", Data: []inference.Embedding{{Index: 0, Vector: []float32{1, 0}}}},
			}}
			service := semanticCachedService(upstream, manager, newFakeEmbedder())
			service.runtime = &cacheRuntimeSource{snapshot: plane.Current()}
			service.cacheConfig.EnableEmbeddingCache = true
			if mode == "embedding" {
				request := &EmbeddingsRequest{AccountID: "account-1", Request: inference.EmbeddingRequest{Model: "openai/embed", Input: inference.EmbeddingInput{Texts: []string{"hello"}}}}
				_, err := service.ProcessEmbeddings(t.Context(), request)
				require.NoError(t, err)
				manager.onRead = func() { source.allowed.Store(false) }
				response, err := service.ProcessEmbeddings(t.Context(), request)
				require.Nil(t, response)
				requireCatalogPermissionRefusal(t, err)
				require.Equal(t, 1, upstream.calls["ProcessEmbeddings"])
				return
			}
			_, err = service.ProcessChatCompletion(t.Context(), semanticChatRequest("account-1", semanticPromptText))
			require.NoError(t, err)
			manager.onRead = func() { source.allowed.Store(false) }
			request := semanticChatRequest("account-1", semanticParaphraseText)
			if mode == "semantic chat" {
				response, err := service.ProcessChatCompletion(t.Context(), request)
				require.Nil(t, response)
				requireCatalogPermissionRefusal(t, err)
			} else {
				request.Request.Stream = true
				stream, err := service.ProcessChatCompletionStream(t.Context(), request)
				if stream != nil {
					require.NoError(t, stream.Close())
				}
				require.Nil(t, stream)
				requireCatalogPermissionRefusal(t, err)
			}
			require.Equal(t, 1, upstream.calls["ProcessChatCompletion"])
			require.Zero(t, upstream.calls["ProcessChatCompletionStream"])
		})
	}
}
