package httpclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		config   Config
		wantErr  bool
	}{
		{
			name:     "default config",
			provider: "test-provider",
			config:   DefaultConfig(),
			wantErr:  false,
		},
		{
			name:     "custom config",
			provider: "custom",
			config: Config{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				MaxConnsPerHost:     50,
				IdleConnTimeout:     30 * time.Second,
				DialTimeout:         10 * time.Second,
				TLSHandshakeTimeout: 5 * time.Second,
				RequestTimeout:      60 * time.Second,
				EnableHTTP2:         true,
				EnableCompression:   true,
				EnableKeepAlives:    true,
				MetricsCollector:    &NoOpMetricsCollector{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.provider, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				if client == nil {
					t.Error("New() returned nil client")
				}
				if client.Provider() != tt.provider {
					t.Errorf("Provider() = %v, want %v", client.Provider(), tt.provider)
				}
				client.Close()
			}
		})
	}
}

func TestClient_Do(t *testing.T) {
	// Create test server
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	}))
	defer server.Close()

	// Create client
	config := DefaultConfig()
	client, err := New("test", config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make request
	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("Expected 1 request, got %d", requestCount)
	}
}

func TestClient_ConcurrentRequests(t *testing.T) {
	// Create test server that tracks concurrent requests
	var maxConcurrent int32
	var currentConcurrent int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&currentConcurrent, 1)
		defer atomic.AddInt32(&currentConcurrent, -1)

		// Update max if needed
		for {
			max := atomic.LoadInt32(&maxConcurrent)
			if current <= max || atomic.CompareAndSwapInt32(&maxConcurrent, max, current) {
				break
			}
		}

		// Simulate some processing time
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with limited connections
	config := DefaultConfig()
	config.MaxConnsPerHost = 5
	config.MaxIdleConnsPerHost = 2 // Must be less than MaxConnsPerHost
	client, err := New("test", config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make concurrent requests
	const numRequests = 20
	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", server.URL, nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("Request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}

	wg.Wait()

	// Check that we respected the connection limit
	maxConc := atomic.LoadInt32(&maxConcurrent)
	if maxConc > int32(config.MaxConnsPerHost) {
		t.Errorf("Max concurrent connections %d exceeded limit %d", maxConc, config.MaxConnsPerHost)
	}
}

// MockMetricsCollector for testing
type MockMetricsCollector struct {
	mu                    sync.Mutex
	connectionCreated     int
	connectionClosed      int
	connectionReused      int
	requestsStarted       int
	requestsCompleted     int
	requestErrors         int
	lastRequestDuration   time.Duration
	lastRequestStatusCode int
}

func (m *MockMetricsCollector) RecordConnectionCreated(provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectionCreated++
}

func (m *MockMetricsCollector) RecordConnectionClosed(provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectionClosed++
}

func (m *MockMetricsCollector) RecordConnectionReused(provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectionReused++
}

func (m *MockMetricsCollector) RecordPoolStats(provider string, stats ConnectionStats) {}

func (m *MockMetricsCollector) RecordRequestStart(provider, method, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestsStarted++
}

func (m *MockMetricsCollector) RecordRequestComplete(provider, method, path string, statusCode int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestsCompleted++
	m.lastRequestDuration = duration
	m.lastRequestStatusCode = statusCode
}

func (m *MockMetricsCollector) RecordRequestError(provider, method, path string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestErrors++
}

func TestMetricsCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with mock metrics collector
	metrics := &MockMetricsCollector{}
	config := DefaultConfig()
	config.MetricsCollector = metrics

	client, err := New("test", config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make a request
	req, _ := http.NewRequest("GET", server.URL+"/test/path", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	// Check metrics
	if metrics.requestsStarted != 1 {
		t.Errorf("Expected 1 request started, got %d", metrics.requestsStarted)
	}
	if metrics.requestsCompleted != 1 {
		t.Errorf("Expected 1 request completed, got %d", metrics.requestsCompleted)
	}
	if metrics.lastRequestStatusCode != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", metrics.lastRequestStatusCode)
	}
	if metrics.lastRequestDuration == 0 {
		t.Error("Expected non-zero request duration")
	}
}

// BenchmarkClient benchmarks client performance
func BenchmarkClient(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	}))
	defer server.Close()

	config := DefaultConfig()
	client, err := New("test", config)
	if err != nil {
		b.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("GET", server.URL, nil)
			resp, err := client.Do(req)
			if err != nil {
				b.Errorf("Request failed: %v", err)
				continue
			}
			resp.Body.Close()
		}
	})
}
