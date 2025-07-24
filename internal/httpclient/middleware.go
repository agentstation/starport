package httpclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// WithRetry adds retry logic to the transport
func WithRetry(maxRetries int, backoff time.Duration) TransportWrapper {
	return func(base RoundTripper) RoundTripper {
		return &retryTransport{
			base:       base,
			maxRetries: maxRetries,
			backoff:    backoff,
		}
	}
}

// retryTransport implements retry logic
type retryTransport struct {
	base       RoundTripper
	maxRetries int
	backoff    time.Duration
}

func (rt *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Save the request body if present
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
	}

	var lastErr error
	backoff := rt.backoff

	for attempt := 0; attempt <= rt.maxRetries; attempt++ {
		// Wait before retry (except first attempt)
		if attempt > 0 {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(backoff):
				backoff = backoff * 2 // Exponential backoff
			}
		}

		// Clone request for each attempt
		newReq := req.Clone(req.Context())
		if bodyBytes != nil {
			newReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			newReq.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(bodyBytes)), nil
			}
		}

		resp, err := rt.base.RoundTrip(newReq)
		if err != nil {
			lastErr = err
			continue
		}

		// Check if response is retryable
		if !isRetryableStatusCode(resp.StatusCode) {
			return resp, nil
		}

		// Consume and close the response body
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		lastErr = &httpError{StatusCode: resp.StatusCode}
	}

	return nil, lastErr
}

// WithRateLimiting adds rate limiting to the transport
func WithRateLimiting(rps int) TransportWrapper {
	return func(base RoundTripper) RoundTripper {
		return &rateLimitTransport{
			base:    base,
			limiter: rate.NewLimiter(rate.Limit(rps), rps),
		}
	}
}

// rateLimitTransport implements rate limiting
type rateLimitTransport struct {
	base    RoundTripper
	limiter *rate.Limiter
}

func (rlt *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Wait for rate limiter
	if err := rlt.limiter.Wait(req.Context()); err != nil {
		return nil, err
	}

	return rlt.base.RoundTrip(req)
}

// WithRequestID adds a unique request ID to each request
func WithRequestID(generator func() string) TransportWrapper {
	if generator == nil {
		generator = generateRequestID
	}

	return func(base RoundTripper) RoundTripper {
		return &requestIDTransport{
			base:      base,
			generator: generator,
		}
	}
}

// requestIDTransport adds request IDs
type requestIDTransport struct {
	base      RoundTripper
	generator func() string
}

func (rit *requestIDTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Add request ID header
	req.Header.Set("X-Request-ID", rit.generator())

	return rit.base.RoundTrip(req)
}

// WithTimeout adds a per-request timeout
func WithTimeout(timeout time.Duration) TransportWrapper {
	return func(base RoundTripper) RoundTripper {
		return &timeoutTransport{
			base:    base,
			timeout: timeout,
		}
	}
}

// timeoutTransport implements per-request timeouts
type timeoutTransport struct {
	base    RoundTripper
	timeout time.Duration
}

func (tt *timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create a new context with timeout
	ctx, cancel := context.WithTimeout(req.Context(), tt.timeout)
	defer cancel()

	// Clone request with new context
	newReq := req.WithContext(ctx)

	return tt.base.RoundTrip(newReq)
}

// ChainTransportWrappers chains multiple transport wrappers
func ChainTransportWrappers(wrappers ...TransportWrapper) TransportWrapper {
	return func(base RoundTripper) RoundTripper {
		// Apply wrappers in reverse order so they execute in the order specified
		for i := len(wrappers) - 1; i >= 0; i-- {
			if wrappers[i] != nil {
				base = wrappers[i](base)
			}
		}
		return base
	}
}

// httpError represents an HTTP error
type httpError struct {
	StatusCode int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	// In production, use a proper UUID generator
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddUint64(&requestCounter, 1))
}

var requestCounter uint64
