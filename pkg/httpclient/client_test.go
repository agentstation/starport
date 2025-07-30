package httpclient

import (
	"errors"
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
			name:     "provider-specific config",
			provider: "openai",
			config:   DefaultProviderConfig("openai"),
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
	config.EnableCircuitBreaker = false // Disable for this test
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
	config.EnableCircuitBreaker = false
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

func TestProviderSpecificConfigs(t *testing.T) {
	providers := []string{"openai", "anthropic", "google", "groq", "azure"}

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			config := DefaultProviderConfig(provider)

			// Verify provider-specific settings are applied
			switch provider {
			case "openai":
				if config.MaxConnsPerHost != 250 {
					t.Errorf("Expected MaxConnsPerHost=250 for OpenAI, got %d", config.MaxConnsPerHost)
				}
			case "anthropic":
				if config.MaxConnsPerHost != 100 {
					t.Errorf("Expected MaxConnsPerHost=100 for Anthropic, got %d", config.MaxConnsPerHost)
				}
			case "google":
				if config.MaxConnsPerHost != 300 {
					t.Errorf("Expected MaxConnsPerHost=300 for Google, got %d", config.MaxConnsPerHost)
				}
			}

			// All should have HTTP/2 enabled
			if !config.EnableHTTP2 {
				t.Errorf("Expected HTTP/2 to be enabled for %s", provider)
			}
		})
	}
}

// TestCircuitBreaker tests circuit breaker functionality
func TestCircuitBreaker(t *testing.T) {
	var failCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&failCount, 1)
		if count <= 5 {
			// First 5 requests fail
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			// Subsequent requests succeed
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	// Create client with circuit breaker
	config := DefaultConfig()
	config.EnableCircuitBreaker = true
	config.CircuitBreakerConfig.FailureThreshold = 3
	config.CircuitBreakerConfig.Timeout = 100 * time.Millisecond

	client, err := New("test", config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make requests that should trigger circuit breaker
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("GET", server.URL, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Circuit should be open now
	req, _ := http.NewRequest("GET", server.URL, nil)
	_, err = client.Do(req)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}

	// Wait for circuit to enter half-open state
	time.Sleep(150 * time.Millisecond)

	// Next request should succeed and close the circuit
	req, _ = http.NewRequest("GET", server.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Errorf("Request failed after circuit timeout: %v", err)
	} else {
		resp.Body.Close()
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
	circuitBreakerOpened  int
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

func (m *MockMetricsCollector) RecordCircuitBreakerOpen(provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.circuitBreakerOpened++
}

func (m *MockMetricsCollector) RecordCircuitBreakerClose(provider string)    {}
func (m *MockMetricsCollector) RecordCircuitBreakerHalfOpen(provider string) {}

func TestMetricsCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with mock metrics collector
	metrics := &MockMetricsCollector{}
	config := DefaultConfig()
	config.MetricsCollector = metrics
	config.EnableCircuitBreaker = false

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
	config.EnableCircuitBreaker = false
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
