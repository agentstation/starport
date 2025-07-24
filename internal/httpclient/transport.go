package httpclient

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// MonitoredTransport wraps an http.RoundTripper to add monitoring and circuit breaking
type MonitoredTransport struct {
	base     http.RoundTripper
	provider string
	metrics  MetricsCollector
	breaker  *CircuitBreaker

	// Connection tracking
	activeRequests int64
}

// RoundTrip implements http.RoundTripper with monitoring
func (t *MonitoredTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Check circuit breaker first
	if t.breaker != nil && !t.breaker.Allow() {
		t.metrics.RecordRequestError(t.provider, req.Method, extractPath(req), ErrCircuitOpen)
		return nil, ErrCircuitOpen
	}

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
		
		// Record circuit breaker failure
		if t.breaker != nil {
			t.breaker.RecordFailure()
		}
		
		return nil, err
	}

	// Record successful request
	t.metrics.RecordRequestComplete(t.provider, req.Method, path, resp.StatusCode, duration)

	// Handle circuit breaker based on status code
	if t.breaker != nil {
		if isRetryableStatusCode(resp.StatusCode) {
			t.breaker.RecordFailure()
		} else {
			t.breaker.RecordSuccess()
		}
	}

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

// connectionTracker tracks connection lifecycle events
type connectionTracker struct {
	transport http.RoundTripper
	metrics   MetricsCollector
	provider  string
}

// RoundTrip tracks connection creation and reuse
func (ct *connectionTracker) RoundTrip(req *http.Request) (*http.Response, error) {
	// Check if this is a new connection by looking at the request context
	// In practice, this would require more sophisticated tracking
	
	resp, err := ct.transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Track connection reuse based on response headers
	// HTTP/2 connections are always reused after initial setup
	if resp.ProtoMajor == 2 || resp.Header.Get("Connection") != "close" {
		ct.metrics.RecordConnectionReused(ct.provider)
	} else {
		ct.metrics.RecordConnectionCreated(ct.provider)
	}

	return resp, nil
}