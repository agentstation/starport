package router

import (
	"context"
	"testing"

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
