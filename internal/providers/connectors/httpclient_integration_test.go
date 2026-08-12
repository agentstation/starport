package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderHTTPTransportContract(t *testing.T) {
	config := ProviderConfig{Timeout: 17 * time.Second, MaxConnections: 23}
	client := newProviderHTTPClient(config)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport = %T, want *http.Transport", client.Transport)
	}

	if transport.MaxIdleConns != config.MaxConnections ||
		transport.MaxIdleConnsPerHost != config.MaxConnections ||
		transport.MaxConnsPerHost != config.MaxConnections {
		t.Fatalf(
			"connection limits = (%d, %d, %d), want %d",
			transport.MaxIdleConns,
			transport.MaxIdleConnsPerHost,
			transport.MaxConnsPerHost,
			config.MaxConnections,
		)
	}
	if transport.ResponseHeaderTimeout != config.Timeout {
		t.Fatalf(
			"response header timeout = %v, want %v",
			transport.ResponseHeaderTimeout,
			config.Timeout,
		)
	}
	if transport.IdleConnTimeout != providerIdleConnectionTimeout ||
		transport.TLSHandshakeTimeout != providerTLSHandshakeTimeout ||
		transport.ExpectContinueTimeout != providerExpectContinueTimeout {
		t.Fatal("provider transport does not use the connector-owned timeout policy")
	}
	if !transport.ForceAttemptHTTP2 || transport.DialContext == nil {
		t.Fatal("provider transport must enable HTTP/2 and a bounded dialer")
	}
	if err := client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("redirect policy error = %v, want %v", err, http.ErrUseLastResponse)
	}
}

func TestProviderHTTPClientHasNoTotalTimeout(t *testing.T) {
	client := newProviderHTTPClient(ProviderConfig{
		Timeout: time.Second, MaxConnections: 10,
	})
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %v, want no transport-owned total timeout", client.Timeout)
	}
}

func TestProviderTransportDoesNotMutateResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Upstream", "preserved")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newProviderHTTPClient(ProviderConfig{
		Timeout: time.Second, MaxConnections: 10,
	})
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.Header.Get("X-Upstream") != "preserved" {
		t.Fatal("provider transport changed an upstream response header")
	}
	for _, name := range []string{"X-HTTP-Client-Provider", "X-HTTP-Client-Duration-Ms"} {
		if value := response.Header.Get(name); value != "" {
			t.Fatalf("provider transport added response header %s=%q", name, value)
		}
	}
}

func TestConnectorPreservesCallerDeadline(t *testing.T) {
	connector, err := NewOpenAIConnector(ProviderConfig{
		BaseURL: "https://provider.invalid", Timeout: time.Second, MaxConnections: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connector.Close()

	var observed time.Time
	connector.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if !ok {
			t.Fatal("provider request has no caller deadline")
		}
		observed = deadline
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"id":"response","object":"chat.completion","choices":[]}`,
			)),
			Request: request,
		}, nil
	})}

	deadline := time.Now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	_, err = connector.Chat(ctx, testChatRequest("openai", "https://provider.invalid/chat/completions"))
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Equal(deadline) {
		t.Fatalf("provider request deadline = %v, want %v", observed, deadline)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

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
