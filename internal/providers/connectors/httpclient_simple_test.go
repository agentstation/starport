package connectors

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

func TestHTTPClientConnectionReuse(t *testing.T) {
	var connectionCount int32
	var requestCount int32

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		_ = json.NewEncoder(w).Encode(ChatResponse{
			ID:      "response",
			Object:  "chat.completion",
			Choices: []Choice{{Message: Message{Role: RoleAssistant, Content: "ok"}}},
		})
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&connectionCount, 1)
		}
	}
	server.Start()
	defer server.Close()

	connector, err := NewOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connector.Close()

	for range 5 {
		if _, err := connector.Chat(context.Background(), testChatRequest("openai", server.URL+"/chat/completions")); err != nil {
			t.Fatal(err)
		}
	}

	if connections, requests := atomic.LoadInt32(&connectionCount), atomic.LoadInt32(&requestCount); connections >= requests {
		t.Fatalf("connection pooling used %d connections for %d requests", connections, requests)
	}
}

func TestHTTPClientCleanup(t *testing.T) {
	for range 3 {
		connector, err := NewAnthropicConnector(ProviderConfig{
			BaseURL:        "http://127.0.0.1",
			APIKey:         "test-key",
			Timeout:        time.Second,
			MaxConnections: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := connector.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func testChatRequest(endpointType, endpointURL string) *ChatRequest {
	return &ChatRequest{
		Model:    "opaque/model@001",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
		Endpoint: InferenceEndpoint{Type: catalogs.EndpointType(endpointType), URL: endpointURL},
	}
}
