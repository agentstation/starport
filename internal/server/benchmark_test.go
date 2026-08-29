package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikey"
)

// BenchmarkProxyHandler measures the overhead of request handling
func BenchmarkProxyHandler(b *testing.B) {
	// Create test server with mock connector
	config := &Config{
		Port:           8080,
		MaxRequestSize: 10 * 1024 * 1024,
	}

	server := newTestServer(b, config)
	const apiKey = "benchmark-gateway-key"
	hash := sha256.Sum256([]byte(apiKey))
	if _, err := server.apiKeys.Create(context.Background(), apikey.APIKey{
		ID: "benchmark-key", Name: "benchmark_key", Hash: hex.EncodeToString(hash[:]),
		Scopes: []string{"*"}, Active: true, CreatedAt: time.Now(),
	}); err != nil {
		b.Fatal(err)
	}

	// Prepare test request
	chatReq := map[string]any{
		"model": "mock/test-model",
		"messages": []map[string]any{
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
			req.Header.Set("Authorization", "Bearer "+apiKey)
			w := httptest.NewRecorder()

			server.router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
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
				req.Header.Set("Authorization", "Bearer "+apiKey)
				w := httptest.NewRecorder()

				server.router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("unexpected status code: %d", w.Code)
				}
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

	server := newTestServer(b, config)

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
	smallReq := map[string]any{
		"model": "gpt-3.5-turbo",
		"messages": []map[string]any{
			{"role": "user", "content": "Hello"},
		},
	}
	smallBody, _ := json.Marshal(smallReq)

	// Large request with many messages
	largeMessages := make([]map[string]any, 100)
	for i := range largeMessages {
		largeMessages[i] = map[string]any{
			"role":    "user",
			"content": "This is a longer message to simulate real-world usage patterns in production environments.",
		}
	}
	largeReq := map[string]any{
		"model":    "gpt-4",
		"messages": largeMessages,
	}
	largeBody, _ := json.Marshal(largeReq)

	b.Run("SmallRequest", func(b *testing.B) {
		b.SetBytes(int64(len(smallBody)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var req map[string]any
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
			var req map[string]any
			if err := json.Unmarshal(largeBody, &req); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
	})
}
