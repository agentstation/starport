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

func (n *NoOpMetricsCollector) RecordConnectionCreated(provider string)                             {}
func (n *NoOpMetricsCollector) RecordConnectionClosed(provider string)                              {}
func (n *NoOpMetricsCollector) RecordConnectionReused(provider string)                              {}
func (n *NoOpMetricsCollector) RecordPoolStats(provider string, stats ConnectionStats)              {}
func (n *NoOpMetricsCollector) RecordRequestStart(provider, method, path string)                    {}
func (n *NoOpMetricsCollector) RecordRequestComplete(provider, method, path string, statusCode int, duration time.Duration) {
}
func (n *NoOpMetricsCollector) RecordRequestError(provider, method, path string, err error)         {}
func (n *NoOpMetricsCollector) RecordCircuitBreakerOpen(provider string)                            {}
func (n *NoOpMetricsCollector) RecordCircuitBreakerClose(provider string)                           {}
func (n *NoOpMetricsCollector) RecordCircuitBreakerHalfOpen(provider string)                        {}

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

// isRetryableError determines if an error should be retried
func isRetryableError(err error) bool {
	// This is a simplified version - in practice, you'd check for
	// specific error types like temporary network errors
	return err != nil
}

// isRetryableStatusCode determines if a status code indicates a retryable error
func isRetryableStatusCode(code int) bool {
	// Retry on 5xx errors and 429 (rate limit)
	return code >= 500 || code == 429
}