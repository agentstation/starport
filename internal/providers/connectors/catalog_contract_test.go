package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/providerauth"
)

func TestExactProviderModelIDIsOpaque(t *testing.T) {
	const providerModelID = "opaque/model@001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/selected/chat", r.URL.Path)
		var request ChatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, providerModelID, request.Model)
		require.NoError(t, json.NewEncoder(w).Encode(ChatResponse{
			ID:      "response",
			Object:  "chat.completion",
			Model:   providerModelID,
			Choices: []Choice{{Message: Message{Role: RoleAssistant, Content: "ok"}}},
		}))
	}))
	defer server.Close()

	connector, err := NewOpenAIConnector(ProviderConfig{BaseURL: server.URL, APIKey: "inference-key"})
	require.NoError(t, err)
	defer connector.Close()
	response, err := connector.Chat(context.Background(), &ChatRequest{
		Model:    providerModelID,
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
		Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/selected/chat"},
	})
	require.NoError(t, err)
	require.Equal(t, providerModelID, response.Model)
}

func TestOfferingEndpointSelectsProtocol(t *testing.T) {
	const providerModelID = "opaque/model@001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/selected/anthropic", r.URL.Path)
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.NotContains(t, request, "model")
		require.Equal(t, "vertex-2023-10-16", request["anthropic_version"])
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "response", "type": "message", "role": "assistant",
			"content": []map[string]string{{"type": "text", "text": "ok"}},
			"usage":   map[string]int{"input_tokens": 1, "output_tokens": 1},
		}))
	}))
	defer server.Close()

	connector, err := NewVertexAIConnector(ProviderConfig{
		BaseURL:  server.URL,
		APIKey:   "inference-token",
		AuthMode: providerauth.ModeStatic,
	})
	require.NoError(t, err)
	defer connector.Close()
	response, err := connector.Chat(context.Background(), &ChatRequest{
		Model:     providerModelID,
		Messages:  []Message{{Role: RoleUser, Content: "hello"}},
		MaxTokens: IntPtr(8),
		Endpoint: InferenceEndpoint{
			Type: catalogs.EndpointTypeAnthropic,
			URL:  server.URL + "/selected/anthropic",
		},
	})
	require.NoError(t, err)
	require.Equal(t, providerModelID, response.Model)
}
