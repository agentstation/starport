package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestGetHTTPClient verifies GetHTTPClient returns the underlying client
func TestGetHTTPClient(t *testing.T) {
	config := DefaultConfig()
	client, err := New("test", config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	httpClient := client.GetHTTPClient()
	if httpClient == nil {
		t.Error("GetHTTPClient returned nil")
	}

	// Verify it's a real http.Client that can make requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Errorf("HTTP client failed to make request: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
}

// TestStats verifies connection statistics
func TestStats(t *testing.T) {
	config := DefaultConfig()
	client, err := New("test-provider", config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	stats := client.Stats()
	if stats.Provider != "test-provider" {
		t.Errorf("Expected provider 'test-provider', got '%s'", stats.Provider)
	}

	// Stats should show -1 for values we can't track
	if stats.IdleConnections != -1 {
		t.Errorf("Expected IdleConnections -1, got %d", stats.IdleConnections)
	}
}

// TestUpdateConfig verifies configuration updates
func TestUpdateConfig(t *testing.T) {
	config := DefaultConfig()
	client, err := New("test", config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Update with new config
	newConfig := DefaultConfig()
	newConfig.MaxIdleConns = 1000
	newConfig.EnableHTTP2 = false

	err = client.UpdateConfig(newConfig)
	if err != nil {
		t.Errorf("UpdateConfig failed: %v", err)
	}

	// Try invalid config
	invalidConfig := Config{
		MaxIdleConns: -1, // Invalid
	}
	err = client.UpdateConfig(invalidConfig)
	if err == nil {
		t.Error("UpdateConfig should fail with invalid config")
	}
}

// TestMonitoredTransportActiveRequests verifies active request tracking
func TestMonitoredTransportActiveRequests(t *testing.T) {
	baseTransport := http.DefaultTransport
	transport := &MonitoredTransport{
		base:     baseTransport,
		provider: "test",
		metrics:  &NoOpMetricsCollector{},
	}

	// Initially should be 0
	if transport.ActiveRequests() != 0 {
		t.Errorf("Expected 0 active requests, got %d", transport.ActiveRequests())
	}

	// Create a server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Start a request in background
	done := make(chan error)
	go func() {
		req, _ := http.NewRequest("GET", server.URL, nil)
		_, err := transport.RoundTrip(req)
		done <- err
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Should have 1 active request
	active := transport.ActiveRequests()
	if active != 1 {
		t.Errorf("Expected 1 active request during processing, got %d", active)
	}

	// Wait for completion
	<-done

	// Should be back to 0
	if transport.ActiveRequests() != 0 {
		t.Errorf("Expected 0 active requests after completion, got %d", transport.ActiveRequests())
	}
}

// TestMonitoredTransportUnwrap verifies Unwrap method
func TestMonitoredTransportUnwrap(t *testing.T) {
	baseTransport := &http.Transport{}
	transport := &MonitoredTransport{
		base:     baseTransport,
		provider: "test",
		metrics:  &NoOpMetricsCollector{},
	}

	unwrapped := transport.Unwrap()
	if unwrapped != baseTransport {
		t.Error("Unwrap did not return the base transport")
	}
}

// TestMonitoredTransportCloseIdleConnections verifies connection cleanup
func TestMonitoredTransportCloseIdleConnections(t *testing.T) {
	transport := &MonitoredTransport{
		base:     &http.Transport{},
		provider: "test",
		metrics:  &NoOpMetricsCollector{},
	}

	// Should not panic
	transport.CloseIdleConnections()
}

// TestMiddlewareChaining verifies transport wrapper chaining
func TestMiddlewareChaining(t *testing.T) {
	var callOrder []string

	wrapper1 := func(rt RoundTripper) RoundTripper {
		return RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			callOrder = append(callOrder, "wrapper1-before")
			resp, err := rt.RoundTrip(r)
			callOrder = append(callOrder, "wrapper1-after")
			return resp, err
		})
	}

	wrapper2 := func(rt RoundTripper) RoundTripper {
		return RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			callOrder = append(callOrder, "wrapper2-before")
			resp, err := rt.RoundTrip(r)
			callOrder = append(callOrder, "wrapper2-after")
			return resp, err
		})
	}

	base := RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		callOrder = append(callOrder, "base")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
		}, nil
	})

	// ChainTransportWrappers returns a wrapper function, apply it to base
	chainedWrapper := ChainTransportWrappers(wrapper1, wrapper2)
	chained := chainedWrapper(base)

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	_, err := chained.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}

	expected := []string{"wrapper1-before", "wrapper2-before", "base", "wrapper2-after", "wrapper1-after"}
	if strings.Join(callOrder, ",") != strings.Join(expected, ",") {
		t.Errorf("Wrong call order. Got: %v, Want: %v", callOrder, expected)
	}
}

// TestWithTimeout verifies timeout middleware
func TestWithTimeout(t *testing.T) {
	// Base transport that delays
	base := RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		select {
		case <-time.After(200 * time.Millisecond):
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})

	// Add timeout shorter than delay
	wrapped := WithTimeout(50 * time.Millisecond)(base)

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	_, err := wrapped.RoundTrip(req)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got: %v", err)
	}
}

// TestRequestIDGeneration verifies request ID generation
func TestRequestIDGeneration(t *testing.T) {
	id1 := generateRequestID()
	id2 := generateRequestID()

	if id1 == "" {
		t.Error("Generated empty request ID")
	}
	if id1 == id2 {
		t.Error("Generated duplicate request IDs")
	}
	if len(id1) < 10 {
		t.Error("Request ID too short")
	}
}

// RoundTripperFunc is a helper to create RoundTripper from a function
type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (f RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// TestConfigValidationEdgeCases tests additional validation scenarios
func TestConfigValidationEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "idle conns exceed max conns",
			config: Config{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 150, // Exceeds MaxConnsPerHost
				MaxConnsPerHost:     100,
				IdleConnTimeout:     90 * time.Second,
				DialTimeout:         30 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
				RequestTimeout:      5 * time.Minute,
			},
			wantErr: true,
			errMsg:  "MaxIdleConnsPerHost cannot exceed MaxConnsPerHost",
		},
		{
			name: "zero response header timeout",
			config: Config{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   50,
				MaxConnsPerHost:       100,
				IdleConnTimeout:       90 * time.Second,
				DialTimeout:           30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 0, // Missing but not validated
				RequestTimeout:        5 * time.Minute,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("Validate() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

// TestExtractPath tests path extraction from requests
func TestExtractPath(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
		want string
	}{
		{
			name: "normal path",
			req:  &http.Request{URL: &url.URL{Path: "/v1/chat/completions"}},
			want: "/v1/chat/completions",
		},
		{
			name: "empty path",
			req:  &http.Request{URL: &url.URL{Path: ""}},
			want: "/",
		},
		{
			name: "nil URL",
			req:  &http.Request{URL: nil},
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractPath(tt.req); got != tt.want {
				t.Errorf("extractPath() = %v, want %v", got, tt.want)
			}
		})
	}
}
