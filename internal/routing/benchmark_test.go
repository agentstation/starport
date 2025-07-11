package routing

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
)

// benchmarkRegistry implements a test registry for benchmarking
type benchmarkRegistry struct {
	connectors map[string]connectors.Connector
}

func (r *benchmarkRegistry) Get(provider string) connectors.Connector {
	return r.connectors[provider]
}

func (r *benchmarkRegistry) List() []string {
	providers := make([]string, 0, len(r.connectors))
	for p := range r.connectors {
		providers = append(providers, p)
	}
	return providers
}

// BenchmarkRoutingDecision measures the time to make a routing decision
func BenchmarkRoutingDecision(b *testing.B) {
	// Create registry with multiple mock connectors
	registry := &benchmarkRegistry{
		connectors: map[string]connectors.Connector{
			"openai":         &mockConnector{name: "openai"},
			"anthropic":      &mockConnector{name: "anthropic"},
			"google-aistudio": &mockConnector{name: "google-aistudio"},
			"groq":           &mockConnector{name: "groq"},
			"mistral":        &mockConnector{name: "mistral"},
		},
	}

	config := &Config{
		MaxRetries:              3,
		RetryDelay:              100 * time.Millisecond,
		BackoffMultiplier:       2.0,
		LatencyAlpha:            0.2,
		LatencyWindowSize:       10,
		EnableCostOptimization:  true,
		MaxCostMultiplier:       3.0,
		MaxLatencyMultiplier:    5.0,
		EnableStickySessions:    true,
		SessionTTL:              30 * time.Minute,
		SessionCleanupInterval:  5 * time.Minute,
		CircuitBreakerThreshold: 3,
		CircuitBreakerTimeout:   30 * time.Second,
	}

	router := NewModelRouter(registry, config)
	ctx := context.Background()

	// Test simple model routing (provider/model format)
	b.Run("SimpleModelRouting", func(b *testing.B) {
		req := &Request{
			Model: "openai/gpt-4",
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.Route(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Test model array routing
	b.Run("ModelArrayRouting", func(b *testing.B) {
		req := &Request{
			Models: []string{"openai/gpt-4", "anthropic/claude-3-opus"},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.Route(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Test auto routing
	b.Run("AutoRouting", func(b *testing.B) {
		req := &Request{
			Model: "openrouter/auto",
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.Route(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Test routing with preferences
	b.Run("WithProviderPreferences", func(b *testing.B) {
		req := &Request{
			Model: "gpt-4",
			ProviderPreferences: &ProviderPreferences{
				Order:  []string{"anthropic", "openai", "google-aistudio"},
				Ignore: []string{"groq"},
			},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.Route(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Test concurrent routing decisions
	b.Run("ConcurrentRouting", func(b *testing.B) {
		req := &Request{
			Model: "openai/gpt-4",
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, err := router.Route(ctx, req)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}

// BenchmarkProviderSelection measures provider selection performance
func BenchmarkProviderSelection(b *testing.B) {
	registry := &benchmarkRegistry{
		connectors: map[string]connectors.Connector{
			"openai":    &mockConnector{name: "openai"},
			"anthropic": &mockConnector{name: "anthropic"},
			"groq":      &mockConnector{name: "groq"},
		},
	}

	router := NewProviderRouter(registry)
	ctx := context.Background()

	// Benchmark basic provider selection
	b.Run("BasicSelection", func(b *testing.B) {
		req := &RoutingRequest{
			Model: "gpt-4",
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.SelectProvider(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Benchmark latency-based routing
	b.Run("LatencyBasedRouting", func(b *testing.B) {
		// Simulate some latency data
		router.(*providerRouter).updateLatency("openai", 50*time.Millisecond)
		router.(*providerRouter).updateLatency("anthropic", 30*time.Millisecond)
		router.(*providerRouter).updateLatency("groq", 10*time.Millisecond)

		req := &RoutingRequest{
			Model: "gpt-4",
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := router.SelectProvider(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkStreamingSetup measures the overhead of setting up streaming
func BenchmarkStreamingSetup(b *testing.B) {
	registry := &benchmarkRegistry{
		connectors: map[string]connectors.Connector{
			"openai": &mockConnector{
				name: "openai",
				chatStreamFunc: func(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error) {
					return &mockStream{}, nil
				},
			},
		},
	}

	router := NewModelRouter(registry, &Config{})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := &Request{
			Model: "openai/gpt-4",
		}
		result, err := router.Route(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
		
		// Simulate setting up a stream
		chatReq := &connectors.ChatRequest{
			Model: "gpt-4",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}
		
		_, err = result.Connector.ChatStream(ctx, chatReq)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// mockStream implements a basic ChatStream for benchmarking
type mockStream struct{}

func (s *mockStream) Read() (*connectors.ChatStreamResponse, error) {
	return &connectors.ChatStreamResponse{
		ID: "test",
		Choices: []connectors.StreamChoice{
			{Delta: connectors.Delta{Content: "test"}},
		},
	}, nil
}

func (s *mockStream) Close() error {
	return nil
}