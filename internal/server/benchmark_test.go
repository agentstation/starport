package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/routing"
	"github.com/go-chi/chi/v5/middleware"
)

// Helper functions for creating pointers
func Float32Ptr(f float32) *float32 { return &f }
func IntPtr(i int) *int             { return &i }

// BenchmarkProxyHandler benchmarks the proxy handler performance
func BenchmarkProxyHandler(b *testing.B) {
	// Create a test server with mock connector
	config := &Config{
		Port:           8080,
		MaxRequestSize: 10 * 1024 * 1024,
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST"},
		},
	}

	// Create registry with mock connector
	reg := registry.New()
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	reg.Register("mock", mockConnector)

	// Create server
	server := New(config, reg)

	// Prepare test request
	chatReq := &proxy.ChatCompletionRequest{
		Model: "mock/test-model",
		Messages: []connectors.Message{
			{Role: "user", Content: "Hello"},
		},
		Temperature: Float32Ptr(0.7),
		MaxTokens:   IntPtr(100),
	}

	b.Run("ChatCompletion", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body, _ := json.Marshal(chatReq)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			if w.Code != http.StatusOK && w.Code != http.StatusUnauthorized {
				b.Fatalf("unexpected status code: %d", w.Code)
			}
		}
	})

	b.Run("WithMiddleware", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body, _ := json.Marshal(chatReq)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			if w.Code != http.StatusOK && w.Code != http.StatusUnauthorized {
				b.Fatalf("unexpected status code: %d", w.Code)
			}
		}
	})

	b.Run("Streaming", func(b *testing.B) {
		streamReq := *chatReq
		streamReq.Stream = true

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body, _ := json.Marshal(&streamReq)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			if w.Code != http.StatusOK && w.Code != http.StatusUnauthorized {
				b.Fatalf("unexpected status code: %d", w.Code)
			}
		}
	})
}

// BenchmarkMiddleware benchmarks individual middleware performance
func BenchmarkMiddleware(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	b.Run("SecurityHeaders", func(b *testing.B) {
		secureHandler := SecurityHeaders(handler)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			w := httptest.NewRecorder()
			secureHandler.ServeHTTP(w, req)
		}
	})

	b.Run("RequestSizeLimiter", func(b *testing.B) {
		limitHandler := RequestSizeLimiter(1024 * 1024)(handler)
		body := bytes.Repeat([]byte("a"), 1000)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
			w := httptest.NewRecorder()
			limitHandler.ServeHTTP(w, req)
		}
	})

	b.Run("LoggingMiddleware", func(b *testing.B) {
		logHandler := LoggingMiddleware(handler)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest("GET", "/test", nil)
			req = req.WithContext(context.WithValue(req.Context(), middleware.RequestIDKey, "test-id"))
			w := httptest.NewRecorder()
			logHandler.ServeHTTP(w, req)
		}
	})
}

// BenchmarkRegistry benchmarks registry operations
func BenchmarkRegistry(b *testing.B) {
	reg := registry.New()

	// Register multiple connectors
	providers := []string{"openai", "anthropic", "google-aistudio", "groq", "mistral"}
	for _, provider := range providers {
		mockConfig := connectors.ProviderConfig{
			BaseURL: "http://" + provider,
		}
		reg.Register(provider, connectors.NewMockConnector(mockConfig))
	}

	b.Run("Get", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			connector, err := reg.Get("openai")
			if err != nil || connector == nil {
				b.Fatal("connector not found")
			}
		}
	})

	b.Run("GetFromModel", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Extract provider from model ID
			provider := "openai"
			connector, err := reg.Get(provider)
			if err != nil || connector == nil {
				b.Fatal("connector not found")
			}
		}
	})

	b.Run("ListProviders", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			providers := reg.ListProviders()
			if len(providers) != 5 {
				b.Fatalf("expected 5 providers, got %d", len(providers))
			}
		}
	})

	b.Run("ConcurrentGet", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				connector, err := reg.Get("openai")
				if err != nil || connector == nil {
					b.Fatal("connector not found")
				}
			}
		})
	})
}

// BenchmarkService benchmarks the service layer
func BenchmarkService(b *testing.B) {
	// Create registry with mock connector
	reg := registry.New()
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	reg.Register("mock", mockConnector)

	// Create service with router
	adapter := newRegistryAdapter(reg)
	router := routing.NewRouter(adapter)
	service := proxy.NewService(reg, router)

	// Prepare test request
	chatReq := &proxy.ChatCompletionRequest{
		Model: "mock/test-model",
		Messages: []connectors.Message{
			{Role: "user", Content: "Hello"},
		},
		Temperature: Float32Ptr(0.7),
		MaxTokens:   IntPtr(100),
	}

	b.Run("ProcessChatCompletion", func(b *testing.B) {
		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp, err := service.ProcessChatCompletion(ctx, chatReq)
			if err != nil || resp == nil {
				b.Fatal("failed to process chat completion")
			}
		}
	})

	b.Run("ListModels", func(b *testing.B) {
		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp, err := service.ListModels(ctx)
			if err != nil || resp == nil {
				b.Fatal("failed to list models")
			}
		}
	})

	b.Run("ListProviders", func(b *testing.B) {
		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			resp, err := service.ListProviders(ctx)
			if err != nil || resp == nil {
				b.Fatal("failed to list providers")
			}
		}
	})
}
