package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
)

// BenchmarkProxyHandler measures the overhead of request handling
func BenchmarkProxyHandler(b *testing.B) {
	// Create test server with mock connector
	config := &Config{
		Port:           8080,
		MaxRequestSize: 10 * 1024 * 1024,
	}

	reg := registry.New()
	
	// Add mock connector
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := connectors.NewMockConnector(mockConfig)
	reg.Register("mock", mockConnector)

	server := New(config, reg)

	// Prepare test request
	chatReq := map[string]interface{}{
		"model": "mock/test-model",
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": "Hello",
			},
		},
	}

	body, _ := json.Marshal(chatReq)

	b.Run("SimpleRequest", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			if w.Code != http.StatusOK && w.Code != http.StatusUnauthorized {
				b.Fatalf("unexpected status code: %d", w.Code)
			}
		}
		b.ReportAllocs()
	})

	b.Run("ConcurrentRequests", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer test-key")
				w := httptest.NewRecorder()

				server.router.ServeHTTP(w, req)
			}
		})
		b.ReportAllocs()
	})
}

// BenchmarkMiddlewareChain measures the overhead of the middleware stack
func BenchmarkMiddlewareChain(b *testing.B) {
	config := &Config{
		Port:           8080,
		MaxRequestSize: 10 * 1024 * 1024,
	}

	reg := registry.New()
	server := New(config, reg)

	b.Run("HealthEndpoint", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest("GET", "/health/live", nil)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				b.Fatalf("unexpected status code: %d", w.Code)
			}
		}
		b.ReportAllocs()
	})
}

// BenchmarkRequestDeserialization measures JSON parsing overhead
func BenchmarkRequestDeserialization(b *testing.B) {
	// Small request
	smallReq := map[string]interface{}{
		"model": "gpt-3.5-turbo",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "Hello"},
		},
	}
	smallBody, _ := json.Marshal(smallReq)

	// Large request with many messages
	largeMessages := make([]map[string]interface{}, 100)
	for i := range largeMessages {
		largeMessages[i] = map[string]interface{}{
			"role":    "user",
			"content": "This is a longer message to simulate real-world usage patterns in production environments.",
		}
	}
	largeReq := map[string]interface{}{
		"model":    "gpt-4",
		"messages": largeMessages,
	}
	largeBody, _ := json.Marshal(largeReq)

	b.Run("SmallRequest", func(b *testing.B) {
		b.SetBytes(int64(len(smallBody)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var req map[string]interface{}
			if err := json.Unmarshal(smallBody, &req); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
	})

	b.Run("LargeRequest", func(b *testing.B) {
		b.SetBytes(int64(len(largeBody)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var req map[string]interface{}
			if err := json.Unmarshal(largeBody, &req); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
	})
}