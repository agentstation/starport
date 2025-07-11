package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/registry"
)

// BenchmarkStreamingFirstToken measures the time to first token in streaming responses
func BenchmarkStreamingFirstToken(b *testing.B) {
	// Create test server with mock streaming connector
	config := &Config{
		Port:           8080,
		MaxRequestSize: 10 * 1024 * 1024,
	}

	// Create registry with streaming mock
	reg := registry.New()
	
	// Create a mock connector that streams immediately
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	mockConnector := &streamingMockConnector{
		MockConnector: connectors.NewMockConnector(mockConfig),
		firstTokenDelay: 0, // No delay for measuring overhead
	}
	reg.Register("mock", mockConnector)

	// Create server
	server := New(config, reg)

	// Prepare streaming request
	chatReq := &proxy.ChatCompletionRequest{
		Model:  "mock/test-model",
		Stream: true,
		Messages: []connectors.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	b.Run("FirstTokenOverhead", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body, _ := json.Marshal(chatReq)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			start := time.Now()
			
			// Start serving the request
			go server.router.ServeHTTP(w, req)
			
			// Measure time to first byte
			firstByte := make([]byte, 1)
			_, err := w.Body.Read(firstByte)
			
			elapsed := time.Since(start)
			
			if err != nil && err != io.EOF {
				b.Fatalf("failed to read first byte: %v", err)
			}
			
			// Track the timing
			b.ReportMetric(float64(elapsed.Nanoseconds()), "ns/first-token")
		}
	})

	// Test with simulated provider latency
	b.Run("WithProviderLatency", func(b *testing.B) {
		// Update mock to simulate 50ms provider latency
		mockConnector.firstTokenDelay = 50 * time.Millisecond
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body, _ := json.Marshal(chatReq)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			start := time.Now()
			server.router.ServeHTTP(w, req)
			elapsed := time.Since(start)
			
			// Verify we got a response
			if w.Code != http.StatusOK && w.Code != http.StatusUnauthorized {
				b.Fatalf("unexpected status code: %d", w.Code)
			}
			
			b.ReportMetric(float64(elapsed.Nanoseconds()), "ns/total")
		}
	})
}

// streamingMockConnector extends MockConnector with streaming support
type streamingMockConnector struct {
	*connectors.MockConnector
	firstTokenDelay time.Duration
}

func (s *streamingMockConnector) ChatStream(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error) {
	return &mockChatStream{
		firstTokenDelay: s.firstTokenDelay,
		chunks: []string{
			"Hello", " from", " streaming", " response",
		},
	}, nil
}

// mockChatStream implements a test stream
type mockChatStream struct {
	firstTokenDelay time.Duration
	chunks          []string
	index           int
}

func (m *mockChatStream) Read() (*connectors.StreamChunk, error) {
	// Simulate provider latency on first token
	if m.index == 0 && m.firstTokenDelay > 0 {
		time.Sleep(m.firstTokenDelay)
	}
	
	if m.index >= len(m.chunks) {
		return nil, io.EOF
	}
	
	chunk := &connectors.StreamChunk{
		Choices: []connectors.StreamChoice{
			{
				Delta: connectors.Delta{
					Content: m.chunks[m.index],
				},
			},
		},
	}
	
	m.index++
	return chunk, nil
}

func (m *mockChatStream) Close() error {
	return nil
}

// BenchmarkStreamingThroughput measures streaming throughput
func BenchmarkStreamingThroughput(b *testing.B) {
	config := &Config{
		Port:           8080,
		MaxRequestSize: 10 * 1024 * 1024,
	}

	reg := registry.New()
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	
	// Create a mock that streams many chunks
	mockConnector := &throughputMockConnector{
		MockConnector: connectors.NewMockConnector(mockConfig),
		chunkCount:    100, // Stream 100 chunks
	}
	reg.Register("mock", mockConnector)

	server := New(config, reg)

	chatReq := &proxy.ChatCompletionRequest{
		Model:  "mock/test-model",
		Stream: true,
		Messages: []connectors.Message{
			{Role: "user", Content: "Generate a long response"},
		},
	}

	b.Run("StreamingThroughput", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body, _ := json.Marshal(chatReq)
			req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			start := time.Now()
			server.router.ServeHTTP(w, req)
			elapsed := time.Since(start)
			
			// Calculate chunks per second
			chunksPerSecond := float64(100) / elapsed.Seconds()
			b.ReportMetric(chunksPerSecond, "chunks/sec")
		}
	})
}

// throughputMockConnector generates many chunks for throughput testing
type throughputMockConnector struct {
	*connectors.MockConnector
	chunkCount int
}

func (t *throughputMockConnector) ChatStream(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error) {
	chunks := make([]string, t.chunkCount)
	for i := range chunks {
		chunks[i] = "word "
	}
	
	return &mockChatStream{
		chunks: chunks,
	}, nil
}