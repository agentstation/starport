package httpclient

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// MonitoredTransport wraps an http.RoundTripper to add request monitoring.
type MonitoredTransport struct {
	base     http.RoundTripper
	provider string
	metrics  MetricsCollector

	// Connection tracking
	activeRequests int64
}

// RoundTrip implements http.RoundTripper with monitoring
func (t *MonitoredTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Track active requests
	atomic.AddInt64(&t.activeRequests, 1)
	defer atomic.AddInt64(&t.activeRequests, -1)

	// Record request start
	start := time.Now()
	path := extractPath(req)
	t.metrics.RecordRequestStart(t.provider, req.Method, path)

	// Perform the request
	resp, err := t.base.RoundTrip(req)

	// Calculate duration
	duration := time.Since(start)

	// Handle errors
	if err != nil {
		t.metrics.RecordRequestError(t.provider, req.Method, path, err)

		return nil, err
	}

	// Record successful request
	t.metrics.RecordRequestComplete(t.provider, req.Method, path, resp.StatusCode, duration)

	// Add custom headers for debugging
	resp.Header.Set("X-HTTP-Client-Provider", t.provider)
	resp.Header.Set("X-HTTP-Client-Duration-Ms", fmt.Sprintf("%d", duration.Milliseconds()))

	return resp, nil
}

// ActiveRequests returns the current number of active requests
func (t *MonitoredTransport) ActiveRequests() int64 {
	return atomic.LoadInt64(&t.activeRequests)
}

// Unwrap returns the underlying transport (useful for testing and debugging)
func (t *MonitoredTransport) Unwrap() http.RoundTripper {
	return t.base
}

// CloseIdleConnections closes idle connections in the underlying transport
func (t *MonitoredTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
		t.metrics.RecordConnectionClosed(t.provider)
	}
}
