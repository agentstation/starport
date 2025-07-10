package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/connectors"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Helper functions for creating pointers
func Float32Ptr(f float32) *float32 { return &f }
func IntPtr(i int) *int { return &i }

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
	
	registry := NewConnectorRegistry()
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	registry.Register("mock", mockConnector)
	
	handler := NewProxyHandler(registry)
	server := New(config, registry)
	
	// Prepare test request
	chatReq := &connectors.ChatRequest{
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
			
			// Create a router and register the handler
			router := chi.NewRouter()
			handler.RegisterRoutes(router)
			router.ServeHTTP(w, req)
			
			if w.Code != http.StatusOK {
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
			
			if w.Code != http.StatusOK {
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
			
			// Create a router and register the handler
			router := chi.NewRouter()
			handler.RegisterRoutes(router)
			router.ServeHTTP(w, req)
			
			if w.Code != http.StatusOK {
				b.Fatalf("unexpected status code: %d", w.Code)
			}
		}
	})
}

// BenchmarkMiddleware benchmarks individual middleware performance
func BenchmarkMiddleware(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
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
		limitHandler := RequestSizeLimiter(1024*1024)(handler)
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

// BenchmarkConnectorRegistry benchmarks connector registry operations
func BenchmarkConnectorRegistry(b *testing.B) {
	registry := NewConnectorRegistry()
	
	// Register multiple connectors
	providers := []string{"openai", "anthropic", "gemini", "groq", "mistral"}
	for _, provider := range providers {
		mockConfig := connectors.ProviderConfig{
			BaseURL: "http://" + provider,
		}
		registry.Register(provider, connectors.NewMockConnector(mockConfig))
	}
	
	b.Run("Get", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			connector := registry.Get("openai")
			if connector == nil {
				b.Fatal("connector not found")
			}
		}
	})
	
	b.Run("GetFromModel", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Extract provider from model ID
			provider := "openai"
			connector := registry.Get(provider)
			if connector == nil {
				b.Fatal("connector not found")
			}
		}
	})
	
	b.Run("List", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			providers := registry.List()
			if len(providers) != 5 {
				b.Fatalf("expected 5 providers, got %d", len(providers))
			}
		}
	})
	
	b.Run("ConcurrentGet", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				connector := registry.Get("openai")
				if connector == nil {
					b.Fatal("connector not found")
				}
			}
		})
	})
}