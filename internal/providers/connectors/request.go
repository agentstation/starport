package connectors

import (
	"net/http"
)

// doRequest performs exactly one provider HTTP attempt. Retry and fallback
// policy belongs to internal/execution.
func doRequest(client *http.Client, request *http.Request) (*http.Response, error) {
	// #nosec G704 -- Starport connectors call request-bound Starmap provider URLs.
	return client.Do(request)
}
