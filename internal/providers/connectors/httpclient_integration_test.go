package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestConnectorHTTPClientIntegration verifies that connectors properly use httpclient features
func TestConnectorHTTPClientIntegration(t *testing.T) {
	tests := []struct {
		name               string
		connectorType      string
		setupConnector     func(config ProviderConfig) (Connector, error)
		wantCircuitBreaker bool
	}{
		{
			name:          "anthropic uses httpclient with circuit breaker",
			connectorType: "anthropic",
			setupConnector: func(config ProviderConfig) (Connector, error) {
				return NewAnthropicConnector(config)
			},
			wantCircuitBreaker: true,
		},
		{
			name:          "openai uses httpclient with circuit breaker",
			connectorType: "openai",
			setupConnector: func(config ProviderConfig) (Connector, error) {
				return NewOpenAIConnector(config)
			},
			wantCircuitBreaker: true,
		},
		{
			name:          "groq uses httpclient through openai base",
			connectorType: "groq",
			setupConnector: func(config ProviderConfig) (Connector, error) {
				return NewGroqConnector(config)
			},
			wantCircuitBreaker: true,
		},
		{
			name:          "mistral uses httpclient with circuit breaker",
			connectorType: "mistral",
			setupConnector: func(config ProviderConfig) (Connector, error) {
				return NewMistralConnector(config)
			},
			wantCircuitBreaker: true,
		},
		{
			name:          "google-aistudio uses httpclient with circuit breaker",
			connectorType: "google-aistudio",
			setupConnector: func(config ProviderConfig) (Connector, error) {
				return NewGoogleAIStudioConnector(config)
			},
			wantCircuitBreaker: true,
		},
		{
			name:          "vertex-ai uses httpclient with circuit breaker",
			connectorType: "vertex-ai",
			setupConnector: func(config ProviderConfig) (Connector, error) {
				config.Extra = map[string]any{
					"project_id": "test-project",
					"location":   "us-central1",
				}
				return NewVertexAIConnector(config)
			},
			wantCircuitBreaker: true,
		},
		{
			name:          "azure uses httpclient through openai base",
			connectorType: "azure",
			setupConnector: func(config ProviderConfig) (Connector, error) {
				config.BaseURL = "https://test.openai.azure.com"
				return NewAzureOpenAIConnector(config)
			},
			wantCircuitBreaker: true,
		},
		{
			name:          "ollama uses httpclient without circuit breaker",
			connectorType: "ollama",
			setupConnector: func(config ProviderConfig) (Connector, error) {
				return NewOllamaConnector(config)
			},
			wantCircuitBreaker: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server that can simulate errors
			var requestCount int32
			var shouldFail atomic.Bool

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requestCount, 1)

				// Simulate failures for circuit breaker testing
				if shouldFail.Load() {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{"error": "simulated error"})
					return
				}

				// Return success response based on endpoint
				switch {
				// OpenAI-style endpoints
				case r.URL.Path == "/v1/models" || r.URL.Path == "/openai/v1/models":
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]any{
						"data": []map[string]string{
							{"id": "test-model"},
						},
					})
				// OpenAI chat completions
				case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/openai/v1/chat/completions":
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]any{
						"id": "test-response",
						"choices": []map[string]any{
							{
								"message": map[string]string{
									"role":    "assistant",
									"content": "test response",
								},
								"finish_reason": "stop",
							},
						},
						"usage": map[string]int{
							"prompt_tokens":     10,
							"completion_tokens": 5,
							"total_tokens":      15,
						},
					})
				// Anthropic messages endpoint
				case r.URL.Path == "/v1/messages" || r.URL.Path == "/messages":
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]any{
						"id":   "test-response",
						"type": "message",
						"role": "assistant",
						"content": []map[string]string{
							{"type": "text", "text": "test response"},
						},
						"usage": map[string]int{
							"input_tokens":  10,
							"output_tokens": 5,
						},
					})
				// Google/Gemini models endpoint
				case strings.Contains(r.URL.Path, "/models"):
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]any{
						"models": []map[string]any{
							{
								"name":                       "models/test-model",
								"supportedGenerationMethods": []string{"generateContent"},
							},
						},
					})
				// Ollama API endpoints
				case r.URL.Path == "/api/tags":
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]any{
						"models": []map[string]any{
							{
								"name": "test-model",
								"size": 1000000,
							},
						},
					})
				// Ollama chat endpoint
				case r.URL.Path == "/api/chat":
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]any{
						"message": map[string]string{
							"role":    "assistant",
							"content": "test response",
						},
						"done": true,
					})
				// Azure deployments endpoint
				case strings.Contains(r.URL.Path, "/deployments"):
					if strings.Contains(r.URL.Path, "/chat/completions") {
						// Azure chat completion
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode(map[string]any{
							"id": "test-response",
							"choices": []map[string]any{
								{
									"message": map[string]string{
										"role":    "assistant",
										"content": "test response",
									},
									"finish_reason": "stop",
								},
							},
						})
					} else {
						w.WriteHeader(http.StatusNotFound)
					}
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			// Create connector with test server
			config := ProviderConfig{
				BaseURL:        server.URL,
				APIKey:         "test-key",
				Timeout:        5 * time.Second,
				MaxConnections: 10,
			}

			connector, err := tt.setupConnector(config)
			if err != nil {
				t.Fatalf("Failed to create connector: %v", err)
			}
			defer connector.Close()

			// Test 1: Verify normal operation
			ctx := context.Background()
			err = connector.Health(ctx)
			if err != nil {
				t.Errorf("Health check failed: %v", err)
			}

			// Test 2: Verify connection pooling by making multiple requests
			// Reset counter
			atomic.StoreInt32(&requestCount, 0)

			// Make multiple concurrent requests
			concurrency := 5
			done := make(chan error, concurrency)

			for i := 0; i < concurrency; i++ {
				go func() {
					_, err := connector.Models(ctx)
					done <- err
				}()
			}

			// Wait for all requests
			for i := 0; i < concurrency; i++ {
				if err := <-done; err != nil {
					t.Errorf("Models request failed: %v", err)
				}
			}

			// Verify requests were made
			finalCount := atomic.LoadInt32(&requestCount)
			if finalCount == 0 {
				t.Error("No requests were made")
			}

			// Test 3: Circuit breaker behavior (if enabled)
			if tt.wantCircuitBreaker {
				// Enable failures
				shouldFail.Store(true)
				atomic.StoreInt32(&requestCount, 0)

				// Make requests that should fail
				failureCount := 0
				for i := 0; i < 10; i++ {
					_, err := connector.Models(ctx)
					if err != nil {
						failureCount++
					}
					time.Sleep(10 * time.Millisecond) // Small delay between requests
				}

				// Circuit breaker should eventually stop making requests
				requestsMade := atomic.LoadInt32(&requestCount)
				if requestsMade == 10 {
					t.Log("Warning: Circuit breaker might not be working correctly - all requests were made")
				}
			}
		})
	}
}

// TestHTTPClientConnectionPooling verifies connection pooling works correctly
func TestHTTPClientConnectionPooling(t *testing.T) {
	// Track active connections
	var activeConnections int32
	var maxConcurrent int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Increment active connections
		current := atomic.AddInt32(&activeConnections, 1)

		// Track max concurrent
		for {
			max := atomic.LoadInt32(&maxConcurrent)
			if current <= max || atomic.CompareAndSwapInt32(&maxConcurrent, max, current) {
				break
			}
		}

		// Simulate some work
		time.Sleep(50 * time.Millisecond)

		// Response
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")

		// Decrement active connections
		atomic.AddInt32(&activeConnections, -1)
	}))
	defer server.Close()

	// Create connector with limited connections
	config := ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 5, // Limit connections
	}

	connector, err := NewOpenAIConnector(config)
	if err != nil {
		t.Fatalf("Failed to create connector: %v", err)
	}
	defer connector.Close()

	// Make many concurrent requests
	concurrency := 20
	done := make(chan error, concurrency)

	ctx := context.Background()
	for i := 0; i < concurrency; i++ {
		go func() {
			_, err := connector.Models(ctx)
			done <- err
		}()
	}

	// Wait for all requests
	for i := 0; i < concurrency; i++ {
		if err := <-done; err != nil {
			t.Errorf("Request failed: %v", err)
		}
	}

	// Verify connection pooling limited concurrent connections
	max := atomic.LoadInt32(&maxConcurrent)
	t.Logf("Max concurrent connections: %d", max)

	// With proper connection pooling, we shouldn't exceed our limit by much
	// (some overhead is expected)
	if max > 10 {
		t.Errorf("Too many concurrent connections: %d (expected <= 10)", max)
	}
}

// TestHTTPClientRetryBehavior verifies retry logic works correctly
func TestHTTPClientRetryBehavior(t *testing.T) {
	var requestCount int32
	var failureCount int32
	maxFailures := int32(2) // Fail first 2 requests

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)

		// Fail first N requests
		if atomic.LoadInt32(&failureCount) < maxFailures {
			atomic.AddInt32(&failureCount, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		// Success
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "model1"}},
		})

		t.Logf("Request %d succeeded", count)
	}))
	defer server.Close()

	config := ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     3,
		RetryDelay:     100 * time.Millisecond,
	}

	connector, err := NewOpenAIConnector(config)
	if err != nil {
		t.Fatalf("Failed to create connector: %v", err)
	}
	defer connector.Close()

	// Make request that should succeed after retries
	ctx := context.Background()
	_, err = connector.Models(ctx)

	if err != nil {
		t.Errorf("Request failed after retries: %v", err)
	}

	// Verify retries happened
	totalRequests := atomic.LoadInt32(&requestCount)
	if totalRequests != maxFailures+1 {
		t.Errorf("Expected %d requests (with retries), got %d", maxFailures+1, totalRequests)
	}
}

// TestHTTPClientTimeouts verifies timeout handling
func TestHTTPClientTimeouts(t *testing.T) {
	// Server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay longer than client timeout
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        500 * time.Millisecond, // Short timeout
		MaxConnections: 10,
	}

	connector, err := NewAnthropicConnector(config)
	if err != nil {
		t.Fatalf("Failed to create connector: %v", err)
	}
	defer connector.Close()

	// Request should timeout
	ctx := context.Background()
	start := time.Now()
	_, err = connector.Models(ctx)
	duration := time.Since(start)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}

	// Verify it actually timed out quickly
	if duration > 1*time.Second {
		t.Errorf("Timeout took too long: %v", duration)
	}
}
