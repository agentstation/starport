package connectors

import (
	"fmt"
	"net/http"
	"time"
)

// doRequestWithRetry performs an HTTP request with retry logic
func doRequestWithRetry(client *http.Client, req *http.Request, config ProviderConfig) (*http.Response, error) {
	var lastErr error
	delay := config.RetryDelay

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-req.Context().Done():
				return nil, ErrContextCanceled
			case <-time.After(delay):
				delay = time.Duration(float64(delay) * config.BackoffMultiplier)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		// Check if response is retryable
		if resp.StatusCode >= 500 || (resp.StatusCode == http.StatusTooManyRequests && attempt < config.MaxRetries) {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			continue
		}

		// Return the response even if it's an error status code
		// Let the connector handle the error parsing
		return resp, nil
	}

	return nil, fmt.Errorf("request failed after %d attempts: %w", config.MaxRetries+1, lastErr)
}