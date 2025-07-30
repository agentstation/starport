package router

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
)

// mockConnectorRegistry implements a test registry for benchmarking
type mockConnectorRegistry struct {
	connectors map[string]connectors.Connector
}

func (r *mockConnectorRegistry) Get(provider string) connectors.Connector {
	return r.connectors[provider]
}

func (r *mockConnectorRegistry) List() []string {
	providers := make([]string, 0, len(r.connectors))
	for p := range r.connectors {
		providers = append(providers, p)
	}
	return providers
}

// BenchmarkSelectModel measures model selection performance
func BenchmarkSelectModel(b *testing.B) {
	// Create registry with multiple mock connectors
	registry := &mockConnectorRegistry{
		connectors: map[string]connectors.Connector{
			"openai":          &mockConnector{name: "openai"},
			"anthropic":       &mockConnector{name: "anthropic"},
			"google-aistudio": &mockConnector{name: "google-aistudio"},
			"groq":            &mockConnector{name: "groq"},
			"mistral":         &mockConnector{name: "mistral"},
		},
	}

	router := New(registry)
	ctx := context.Background()

	b.Run("SimpleModelSelection", func(b *testing.B) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Model: "openai/gpt-4",
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _, err := router.SelectModel(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
	})

	b.Run("WithProviderPreferences", func(b *testing.B) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{"openai/gpt-4", "anthropic/claude-3-opus", "groq/llama-3-70b"},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
			ProviderPreferences: &ProviderPreferences{
				Order:  []string{"anthropic", "openai", "groq"},
				Ignore: []string{"mistral"},
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _, err := router.SelectModel(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
	})
}

// BenchmarkRouteWithFallback measures routing with fallback performance
func BenchmarkRouteWithFallback(b *testing.B) {
	registry := &mockConnectorRegistry{
		connectors: map[string]connectors.Connector{
			"openai":    &mockConnector{name: "openai"},
			"anthropic": &mockConnector{name: "anthropic"},
			"groq":      &mockConnector{name: "groq"},
		},
	}

	router := New(registry)
	ctx := context.Background()

	b.Run("SingleModel", func(b *testing.B) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Model: "openai/gpt-4",
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.RouteWithFallback(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
	})

	b.Run("ModelArray", func(b *testing.B) {
		req := &Request{
			ChatRequest: &connectors.ChatRequest{
				Models: []string{"openai/gpt-4", "anthropic/claude-3-opus"},
				Messages: []connectors.Message{
					{Role: "user", Content: "Hello"},
				},
			},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.RouteWithFallback(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
	})
}

// BenchmarkLatencyTracking measures latency tracking overhead
func BenchmarkLatencyTracking(b *testing.B) {
	tracker := NewLatencyTracker(0.2, 5)

	b.Run("RecordLatency", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tracker.RecordLatency("openai", 50*time.Millisecond)
		}
		b.ReportAllocs()
	})

	b.Run("GetLatency", func(b *testing.B) {
		// Pre-populate some data
		for i := 0; i < 10; i++ {
			tracker.RecordLatency("openai", 50*time.Millisecond)
			tracker.RecordLatency("anthropic", 30*time.Millisecond)
			tracker.RecordLatency("groq", 10*time.Millisecond)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = tracker.GetLatency("openai")
		}
		b.ReportAllocs()
	})
}

// BenchmarkProviderHealth measures provider health tracking overhead
func BenchmarkProviderHealth(b *testing.B) {
	registry := &mockConnectorRegistry{
		connectors: map[string]connectors.Connector{
			"openai":    &mockConnector{name: "openai"},
			"anthropic": &mockConnector{name: "anthropic"},
			"groq":      &mockConnector{name: "groq"},
		},
	}

	router := New(registry).(*modelRouter)

	b.Run("RecordSuccess", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			router.recordProviderSuccess("openai")
		}
		b.ReportAllocs()
	})

	b.Run("RecordFailure", func(b *testing.B) {
		err := &connectors.APIError{StatusCode: 500, Message: "Internal error"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			router.recordProviderFailure("openai", err)
		}
		b.ReportAllocs()
	})

	b.Run("IsProviderHealthy", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = router.isProviderHealthy("openai")
		}
		b.ReportAllocs()
	})
}

// BenchmarkConcurrentRouting measures concurrent routing performance
func BenchmarkConcurrentRouting(b *testing.B) {
	registry := &mockConnectorRegistry{
		connectors: map[string]connectors.Connector{
			"openai":    &mockConnector{name: "openai"},
			"anthropic": &mockConnector{name: "anthropic"},
			"groq":      &mockConnector{name: "groq"},
		},
	}

	router := New(registry)
	ctx := context.Background()

	req := &Request{
		ChatRequest: &connectors.ChatRequest{
			Model: "openai/gpt-4",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		},
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, err := router.SelectModel(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.ReportAllocs()
}
