package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConnectorContract(t *testing.T) {
	t.Run("mock", func(t *testing.T) {
		runConnectorContract(t, "http://mock.local/v1", func(t *testing.T) Connector {
			t.Helper()
			return NewMockConnector(ProviderConfig{BaseURL: "http://mock.local", APIKey: "test-key"})
		})
	})

	t.Run("openai compatible", func(t *testing.T) {
		server := newOpenAICompatibleContractServer(t)
		runConnectorContract(t, server.URL+"/v1", func(t *testing.T) Connector {
			t.Helper()
			connector, err := NewOpenAIConnector(ProviderConfig{
				BaseURL: server.URL + "/v1",
				APIKey:  "test-key",
				Timeout: 2 * time.Second,
			})
			if err != nil {
				t.Fatalf("new openai connector: %v", err)
			}
			return connector
		})
	})
}

func runConnectorContract(t *testing.T, endpointBaseURL string, newConnector func(*testing.T) Connector) {
	t.Helper()
	ctx := context.Background()
	connector := newConnector(t)
	t.Cleanup(func() { _ = connector.Close() })

	if connector.Name() == "" {
		t.Fatal("connector name must not be empty")
	}

	chatResp, err := connector.Chat(ctx, &ChatRequest{
		Model: "openai/gpt-4o-mini",
		Endpoint: InferenceEndpoint{
			Type: "openai",
			URL:  endpointBaseURL + "/chat/completions",
		},
		Messages: []Message{
			{Role: RoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if chatResp == nil || len(chatResp.Choices) == 0 {
		t.Fatalf("chat response must include at least one choice: %#v", chatResp)
	}

	stream, err := connector.ChatStream(ctx, &ChatRequest{
		Model:  "openai/gpt-4o-mini",
		Stream: true,
		Endpoint: InferenceEndpoint{
			Type: "openai",
			URL:  endpointBaseURL + "/chat/completions",
		},
		Messages: []Message{
			{Role: RoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}
	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream first chunk: %v", err)
	}
	if chunk == nil {
		t.Fatal("stream first chunk must not be nil")
	}
	for {
		_, err = stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("stream recv: %v", err)
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream close: %v", err)
	}

	embeddingsResp, err := connector.Embeddings(ctx, &EmbeddingsRequest{
		Model: "openai/text-embedding-3-small",
		Input: "hello",
		Endpoint: InferenceEndpoint{
			Type: "openai",
			URL:  endpointBaseURL + "/embeddings",
		},
	})
	if err != nil {
		t.Fatalf("embeddings: %v", err)
	}
	if embeddingsResp == nil || len(embeddingsResp.Data) == 0 {
		t.Fatalf("embeddings response must include data: %#v", embeddingsResp)
	}

	if err := connector.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func newOpenAICompatibleContractServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			if r.URL.Query().Get("stream") == "true" {
				writeContractStream(w)
				return
			}
			var req ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode chat request: %v", err)
			}
			if req.Stream {
				writeContractStream(w)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl-contract",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   req.Model,
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]string{
							"role":    "assistant",
							"content": "ok",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     1,
					"completion_tokens": 1,
					"total_tokens":      2,
				},
			})
		case "/v1/embeddings":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{
						"object":    "embedding",
						"index":     0,
						"embedding": []float64{0.1, 0.2, 0.3},
					},
				},
				"model": "text-embedding-3-small",
				"usage": map[string]int{
					"prompt_tokens": 1,
					"total_tokens":  1,
				},
			})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{
						"id":       "gpt-4o-mini",
						"object":   "model",
						"created":  time.Now().Unix(),
						"owned_by": "openai",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeContractStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-contract\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
