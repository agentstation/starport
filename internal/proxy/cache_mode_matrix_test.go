package proxy

import (
	"fmt"
	"testing"
	"time"

	bytecache "github.com/agentstation/starport/internal/cache"
	"github.com/stretchr/testify/require"
)

func TestProductionCacheSemanticModeMatrix(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, mode := range []string{"disabled", "exact", "semantic", "request opt out", "expired semantic"} {
			t.Run(fmt.Sprintf("%s/stream=%t", mode, streaming), func(t *testing.T) {
				cfg := bytecache.ManagerConfig{}
				cfg.Responses.TTL = time.Second
				manager, err := bytecache.NewCacheManager(cfg, nil)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, manager.Close()) })
				upstream := &mockProxyImpl{chatResponse: canonicalChatResponse()}
				embedder := newFakeEmbedder()
				service := semanticCachedService(upstream, manager, embedder)
				service.cacheConfig.EnableChatCache = mode != "disabled"
				service.cacheConfig.EnableSemanticCache = mode != "exact"
				call := func(text string) string {
					request := semanticChatRequest("account", text)
					request.Request.Stream = streaming
					request.SemanticCache = mode != "request opt out"
					if streaming {
						stream, err := service.ProcessChatCompletionStream(t.Context(), request)
						require.NoError(t, err)
						readAllEvents(t, stream)
						require.NoError(t, stream.Close())
						if status, ok := stream.(CacheStatusProvider); ok {
							return status.GetCacheStatus()
						}
						return ""
					}
					response, err := service.ProcessChatCompletion(t.Context(), request)
					require.NoError(t, err)
					return response.CacheStatus
				}
				call(semanticPromptText)
				if mode != "disabled" {
					require.Eventually(t, func() bool { return manager.FillStatus().RetainedEntries == 0 }, time.Second, time.Millisecond)
					require.Positive(t, manager.FillStatus().CompletedFills)
				}
				text := semanticPromptText
				if mode == "semantic" || mode == "request opt out" || mode == "expired semantic" {
					text = semanticParaphraseText
				}
				if mode == "expired semantic" {
					first := semanticChatRequest("account", semanticPromptText)
					first.Request.Stream = streaming
					key, err := service.generateChatCacheKey(t.Context(), first)
					require.NoError(t, err)
					_, found, err := manager.GetResponse(t.Context(), key)
					require.NoError(t, err)
					require.True(t, found, "expiry case must start with a real exact entry")
					require.Eventually(t, func() bool { _, found, err := manager.GetResponse(t.Context(), key); return err == nil && !found }, 3*time.Second, 5*time.Millisecond)
				}
				status := call(text)
				wantHit := mode == "exact" || mode == "semantic"
				require.Equal(t, wantHit, status == CacheStatusHit)
				method := "ProcessChatCompletion"
				if streaming {
					method += "Stream"
				}
				wantCalls := 2
				if wantHit {
					wantCalls = 1
				}
				require.Equal(t, wantCalls, upstream.calls[method])
				if mode == "disabled" || mode == "exact" || mode == "request opt out" {
					require.Zero(t, embedder.calls, "ineligible modes must not perform paid semantic embedding work")
				}
				if mode == "disabled" {
					require.Zero(t, manager.FillStatus().CompletedFills)
				}
			})
		}
	}
}
