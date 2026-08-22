package router

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestRouteStreamUsesExecutorFallbackBeforeFirstEvent(t *testing.T) {
	openAI := connectors.NewMockConnector(connectors.ProviderConfig{})
	openAI.SetStreamError(&connectors.APIError{
		Provider:   "openai",
		StatusCode: 429,
		Message:    "rate limited",
	})
	anthropic := connectors.NewMockConnector(connectors.ProviderConfig{})
	anthropic.SetStreamChunks([]connectors.ChatStreamChunk{{
		ID:    "chatcmpl-stream",
		Model: "claude-3",
		Choices: []connectors.StreamChoice{{
			Index: 0,
			Delta: connectors.MessageDelta{Content: "ok"},
		}},
	}})

	router := New(&mockRegistry{connectors: map[string]connectors.Connector{
		"openai":    openAI,
		"anthropic": anthropic,
	}})
	stream, err := router.RouteStream(context.Background(), &Request{
		ChatRequest: &connectors.ChatRequest{
			Models: []string{"openai/gpt-4o", "anthropic/claude-3"},
			Stream: true,
		},
	})
	require.NoError(t, err)
	defer stream.Close()

	event, err := stream.Read()
	require.NoError(t, err)
	require.Equal(t, inference.StreamDelta, event.Kind)
	require.Equal(t, "anthropic/claude-3", event.ModelUsed)
	require.Len(t, stream.Attempts(), 2)
	require.Equal(t, "failed", string(stream.Attempts()[0].State))
	require.True(t, stream.Committed())
}

// A provider can reject a streaming request inside an established 200
// stream (an SSE error frame before any content). The attempt must fail
// over to the next route exactly like an establishment failure.
func TestRouteStreamFailsOverWhenFirstEventIsError(t *testing.T) {
	openAI := connectors.NewMockConnector(connectors.ProviderConfig{})
	openAI.SetStreamChunks(nil)
	openAI.SetStreamRecvError(&connectors.APIError{
		Provider:   "openai",
		StatusCode: 429,
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached",
	})
	anthropic := connectors.NewMockConnector(connectors.ProviderConfig{})
	anthropic.SetStreamChunks([]connectors.ChatStreamChunk{{
		ID:    "chatcmpl-stream",
		Model: "claude-3",
		Choices: []connectors.StreamChoice{{
			Index: 0,
			Delta: connectors.MessageDelta{Content: "ok"},
		}},
	}})

	router := New(&mockRegistry{connectors: map[string]connectors.Connector{
		"openai":    openAI,
		"anthropic": anthropic,
	}})
	stream, err := router.RouteStream(context.Background(), &Request{
		ChatRequest: &connectors.ChatRequest{
			Models: []string{"openai/gpt-4o", "anthropic/claude-3"},
			Stream: true,
		},
	})
	require.NoError(t, err)
	defer stream.Close()

	event, err := stream.Read()
	require.NoError(t, err)
	require.Equal(t, "anthropic/claude-3", event.ModelUsed)
	require.Len(t, stream.Attempts(), 2)
	require.Equal(t, "failed", string(stream.Attempts()[0].State))
}

// With no fallback route, a pre-content stream error must surface as the
// normalized failure so the HTTP layer writes the provider status (429),
// not an empty 200 stream.
func TestRouteStreamSurfacesFirstEventRateLimit(t *testing.T) {
	openAI := connectors.NewMockConnector(connectors.ProviderConfig{})
	openAI.SetStreamChunks(nil)
	openAI.SetStreamRecvError(&connectors.APIError{
		Provider:   "openai",
		StatusCode: 429,
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached",
	})

	router := New(&mockRegistry{connectors: map[string]connectors.Connector{
		"openai": openAI,
	}})
	stream, err := router.RouteStream(context.Background(), &Request{
		ChatRequest: &connectors.ChatRequest{
			Models: []string{"openai/gpt-4o"},
			Stream: true,
		},
	})
	require.Error(t, err)
	require.Nil(t, stream)
	var normalized *failure.Failure
	require.ErrorAs(t, err, &normalized)
	require.Equal(t, failure.RateLimit, normalized.Kind())
	require.Equal(t, 429, normalized.ProviderDetails().StatusCode)
}

// A stream that ends before its first event carries no completion. It is a
// provider failure, never an empty 200 success.
func TestRouteStreamRejectsEmptyProviderStream(t *testing.T) {
	openAI := connectors.NewMockConnector(connectors.ProviderConfig{})
	openAI.SetStreamChunks(nil)

	router := New(&mockRegistry{connectors: map[string]connectors.Connector{
		"openai": openAI,
	}})
	stream, err := router.RouteStream(context.Background(), &Request{
		ChatRequest: &connectors.ChatRequest{
			Models: []string{"openai/gpt-4o"},
			Stream: true,
		},
	})
	require.Error(t, err)
	require.Nil(t, stream)
	var normalized *failure.Failure
	require.ErrorAs(t, err, &normalized)
	require.Equal(t, failure.ProviderUnavailable, normalized.Kind())
}

func TestPrepareChatAttemptSelectsCatalogStreamURL(t *testing.T) {
	req := &Request{ChatRequest: &connectors.ChatRequest{Model: "definition/model"}}
	route := routing.Route{
		ProviderModelID: "opaque/model@001",
		Endpoint: routing.Endpoint{
			Protocol:  "google-cloud",
			URL:       "https://provider.test/invoke",
			StreamURL: "https://provider.test/stream-invoke",
		},
	}

	nonStreaming := prepareChatAttempt(req, route, false)
	require.Equal(t, "https://provider.test/invoke", nonStreaming.Endpoint.URL)
	streaming := prepareChatAttempt(req, route, true)
	require.Equal(t, "https://provider.test/stream-invoke", streaming.Endpoint.URL)
	require.Equal(t, "opaque/model@001", streaming.Model)
}
