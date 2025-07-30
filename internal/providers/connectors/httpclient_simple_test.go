package connectors

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestConnectorsUseHTTPClient verifies all connectors properly use httpclient
func TestConnectorsUseHTTPClient(t *testing.T) {
	// Create a simple test server that always returns success
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	}))
	defer server.Close()

	tests := []struct {
		name            string
		createConnector func() (Connector, error)
	}{
		{
			name: "Anthropic",
			createConnector: func() (Connector, error) {
				return NewAnthropicConnector(ProviderConfig{
					BaseURL:        server.URL,
					APIKey:         "test-key",
					Timeout:        5 * time.Second,
					MaxConnections: 10,
				})
			},
		},
		{
			name: "OpenAI",
			createConnector: func() (Connector, error) {
				return NewOpenAIConnector(ProviderConfig{
					BaseURL:        server.URL,
					APIKey:         "test-key",
					Timeout:        5 * time.Second,
					MaxConnections: 10,
				})
			},
		},
		{
			name: "Groq",
			createConnector: func() (Connector, error) {
				return NewGroqConnector(ProviderConfig{
					BaseURL:        server.URL,
					APIKey:         "test-key",
					Timeout:        5 * time.Second,
					MaxConnections: 10,
				})
			},
		},
		{
			name: "Mistral",
			createConnector: func() (Connector, error) {
				return NewMistralConnector(ProviderConfig{
					BaseURL:        server.URL,
					APIKey:         "test-key",
					Timeout:        5 * time.Second,
					MaxConnections: 10,
				})
			},
		},
		{
			name: "Google AI Studio",
			createConnector: func() (Connector, error) {
				return NewGoogleAIStudioConnector(ProviderConfig{
					BaseURL:        server.URL,
					APIKey:         "test-key",
					Timeout:        5 * time.Second,
					MaxConnections: 10,
				})
			},
		},
		{
			name: "Vertex AI",
			createConnector: func() (Connector, error) {
				return NewVertexAIConnector(ProviderConfig{
					BaseURL:        server.URL,
					APIKey:         "test-key",
					Timeout:        5 * time.Second,
					MaxConnections: 10,
					Extra: map[string]any{
						"project_id": "test-project",
						"location":   "us-central1",
					},
				})
			},
		},
		{
			name: "Azure",
			createConnector: func() (Connector, error) {
				return NewAzureOpenAIConnector(ProviderConfig{
					BaseURL:        server.URL,
					APIKey:         "test-key",
					Timeout:        5 * time.Second,
					MaxConnections: 10,
				})
			},
		},
		{
			name: "Ollama",
			createConnector: func() (Connector, error) {
				return NewOllamaConnector(ProviderConfig{
					BaseURL:        server.URL,
					APIKey:         "",
					Timeout:        5 * time.Second,
					MaxConnections: 10,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset counter
			atomic.StoreInt32(&requestCount, 0)

			// Create connector
			connector, err := tt.createConnector()
			if err != nil {
				t.Fatalf("Failed to create connector: %v", err)
			}
			defer connector.Close()

			// Make a simple request to verify httpclient is being used
			// We'll use the models endpoint as it's generally simpler
			ctx := context.Background()
			_, _ = connector.Models(ctx) // Ignore error, we just want to see if request was made

			// Verify at least one request was made
			count := atomic.LoadInt32(&requestCount)

			// Special cases: some connectors don't make HTTP requests for Models()
			if tt.name == "Vertex AI" || tt.name == "Azure" {
				// These connectors return static model lists, so no HTTP request expected
				t.Logf("%s connector doesn't make HTTP requests for Models() (returns static list)", tt.name)
			} else {
				if count == 0 {
					t.Error("No HTTP requests were made - connector may not be using httpclient properly")
				}
				// Success - the connector is using httpclient to make requests
				t.Logf("%s connector made %d requests using httpclient", tt.name, count)
			}
		})
	}
}

// TestHTTPClientConnectionReuse verifies connection pooling is working
func TestHTTPClientConnectionReuse(t *testing.T) {
	// Create server that tracks connections
	var connectionCount int32
	var requestCount int32

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": [{"id": "model1"}]}`))
	}))

	// Track new connections
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&connectionCount, 1)
		}
	}
	server.Start()
	defer server.Close()

	// Create connector with httpclient
	config := ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	}

	connector, err := NewOpenAIConnector(config)
	if err != nil {
		t.Fatalf("Failed to create connector: %v", err)
	}
	defer connector.Close()

	// Make multiple requests
	ctx := context.Background()
	requestsToMake := 5

	for i := 0; i < requestsToMake; i++ {
		_, err := connector.Models(ctx)
		if err != nil {
			t.Logf("Request %d failed: %v", i+1, err)
		}
	}

	// Check results
	totalRequests := atomic.LoadInt32(&requestCount)
	totalConnections := atomic.LoadInt32(&connectionCount)

	t.Logf("Made %d requests using %d connections", totalRequests, totalConnections)

	// With connection pooling, we should have fewer connections than requests
	if totalConnections >= totalRequests {
		t.Errorf("Connection pooling not working: %d connections for %d requests", totalConnections, totalRequests)
	}
}

// TestHTTPClientCleanup verifies connectors properly close connections
func TestHTTPClientCleanup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": []}`))
	}))
	defer server.Close()

	config := ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	}

	// Create and close multiple connectors
	for i := 0; i < 3; i++ {
		connector, err := NewAnthropicConnector(config)
		if err != nil {
			t.Fatalf("Failed to create connector: %v", err)
		}

		// Make a request
		ctx := context.Background()
		_, _ = connector.Models(ctx)

		// Close should clean up connections
		err = connector.Close()
		if err != nil {
			t.Errorf("Failed to close connector: %v", err)
		}
	}

	// If we get here without hanging or errors, cleanup is working
	t.Log("Connector cleanup working correctly")
}
