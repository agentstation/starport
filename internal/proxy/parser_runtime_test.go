package proxy

import (
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

func TestParserRuntimeLeaseLifetime(t *testing.T) {
	t.Run("completion", func(t *testing.T) {
		source := &cacheRuntimeSource{}
		service := &proxy{registry: source, router: newRecognizingRouter("invoice")}
		_, err := service.ProcessChatCompletion(t.Context(), parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
		require.NoError(t, err)
		require.Len(t, source.leases, 1)
		require.True(t, source.lastLease(t).released.Load())
	})
	t.Run("recognition error", func(t *testing.T) {
		source := &cacheRuntimeSource{}
		service := &proxy{registry: source, router: newRecognizingRouter()}
		_, err := service.ProcessChatCompletion(t.Context(), parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
		require.Error(t, err)
		require.Len(t, source.leases, 1)
		require.True(t, source.lastLease(t).released.Load())
	})
	t.Run("stream close", func(t *testing.T) {
		source := &cacheRuntimeSource{}
		service := &proxy{registry: source, router: &streamCapturingRouter{}}
		request := parsedRequest(t, "handwritten.pdf", inference.ParserEngineNative)
		request.Request.Stream = true
		stream, err := service.ProcessChatCompletionStream(t.Context(), request)
		require.NoError(t, err)
		require.Len(t, source.leases, 1)
		require.False(t, source.lastLease(t).released.Load())
		require.NoError(t, stream.Close())
		require.True(t, source.lastLease(t).released.Load())
	})
	t.Run("inherited", func(t *testing.T) {
		source := &cacheRuntimeSource{}
		inherited := &cacheRuntimeLease{}
		service := &proxy{registry: source, router: newRecognizingRouter("invoice")}
		_, err := service.ProcessChatCompletion(connectors.ContextWithRuntimeLease(t.Context(), inherited), parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
		require.NoError(t, err)
		require.Empty(t, source.leases)
		require.False(t, inherited.released.Load())
	})
}
