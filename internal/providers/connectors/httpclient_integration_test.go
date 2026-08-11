package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPClientConnectionPooling(t *testing.T) {
	var activeConnections int32
	var maxConcurrent int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := atomic.AddInt32(&activeConnections, 1)
		defer atomic.AddInt32(&activeConnections, -1)
		for {
			maximum := atomic.LoadInt32(&maxConcurrent)
			if current <= maximum || atomic.CompareAndSwapInt32(&maxConcurrent, maximum, current) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(ChatResponse{
			ID:      "response",
			Object:  "chat.completion",
			Choices: []Choice{{Message: Message{Role: RoleAssistant, Content: "ok"}}},
		})
	}))
	defer server.Close()

	connector, err := NewOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL,
		Timeout:        5 * time.Second,
		MaxConnections: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connector.Close()

	done := make(chan error, 20)
	for range 20 {
		go func() {
			_, err := connector.Chat(context.Background(), testChatRequest("openai", server.URL+"/chat/completions"))
			done <- err
		}()
	}
	for range 20 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if maximum := atomic.LoadInt32(&maxConcurrent); maximum > 10 {
		t.Fatalf("maximum concurrent connections = %d, want <= 10", maximum)
	}
}

func TestHTTPClientSingleAttempt(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"unavailable"}}`))
	}))
	defer server.Close()

	connector, err := NewOpenAIConnector(ProviderConfig{
		BaseURL:        server.URL,
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connector.Close()

	if _, err := connector.Chat(context.Background(), testChatRequest("openai", server.URL+"/chat/completions")); err == nil {
		t.Fatal("Chat() error = nil, want provider error")
	}
	if requests := atomic.LoadInt32(&requestCount); requests != 1 {
		t.Fatalf("provider requests = %d, want 1", requests)
	}
}

func TestHTTPClientTimeouts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	connector, err := NewAnthropicConnector(ProviderConfig{
		BaseURL:        server.URL,
		Timeout:        500 * time.Millisecond,
		MaxConnections: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connector.Close()

	start := time.Now()
	_, err = connector.Chat(context.Background(), testChatRequest("anthropic", server.URL+"/messages"))
	if err == nil {
		t.Fatal("Chat() error = nil, want timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout took %v, want <= 1s", elapsed)
	}
}
