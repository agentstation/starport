package httpclient

import (
	"net/http"
	"time"
)

// MetricsCollector defines the interface for collecting HTTP client metrics
type MetricsCollector interface {
	// Connection pool metrics
	RecordConnectionCreated(provider string)
	RecordConnectionClosed(provider string)
	RecordConnectionReused(provider string)
	RecordPoolStats(provider string, stats ConnectionStats)

	// Request lifecycle metrics
	RecordRequestStart(provider string, method string, path string)
	RecordRequestComplete(provider string, method string, path string, statusCode int, duration time.Duration)
	RecordRequestError(provider string, method string, path string, err error)

	// Circuit breaker metrics
	RecordCircuitBreakerOpen(provider string)
	RecordCircuitBreakerClose(provider string)
	RecordCircuitBreakerHalfOpen(provider string)
}

// ConnectionStats represents current connection pool statistics
type ConnectionStats struct {
	Provider          string
	IdleConnections   int
	ActiveConnections int
	TotalConnections  int
	WaitingRequests   int
}

// NoOpMetricsCollector is a no-op implementation of MetricsCollector
type NoOpMetricsCollector struct{}

var _ MetricsCollector = (*NoOpMetricsCollector)(nil)

// RecordConnectionCreated does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordConnectionCreated(_ string) {}

// RecordConnectionClosed does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordConnectionClosed(_ string) {}

// RecordConnectionReused does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordConnectionReused(_ string) {}

// RecordPoolStats does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordPoolStats(_ string, _ ConnectionStats) {}

// RecordRequestStart does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordRequestStart(_, _, _ string) {}

// RecordRequestComplete does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordRequestComplete(_, _, _ string, _ int, _ time.Duration) {}

// RecordRequestError does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordRequestError(_, _, _ string, _ error) {}

// RecordCircuitBreakerOpen does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordCircuitBreakerOpen(_ string) {}

// RecordCircuitBreakerClose does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordCircuitBreakerClose(_ string) {}

// RecordCircuitBreakerHalfOpen does nothing in NoOpMetricsCollector
func (n *NoOpMetricsCollector) RecordCircuitBreakerHalfOpen(_ string) {}

// RoundTripper is an interface for HTTP round trippers (copied to avoid circular import)
type RoundTripper interface {
	RoundTrip(*http.Request) (*http.Response, error)
}

// extractPath extracts a sanitized path from the request URL for metrics
func extractPath(req *http.Request) string {
	if req.URL == nil {
		return "unknown"
	}

	// For LLM providers, we typically want to track the API endpoint
	// e.g., "/v1/chat/completions" or "/v1/embeddings"
	path := req.URL.Path
	if path == "" {
		path = "/"
	}

	return path
}

// isRetryableStatusCode determines if a status code indicates a retryable error
func isRetryableStatusCode(code int) bool {
	// Retry on 5xx errors and 429 (rate limit)
	return code >= 500 || code == 429
}
