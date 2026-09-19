package connectors

import (
	providerauth "github.com/agentstation/starport/internal/providers/auth"
	"net/http"
)

// doRequest performs exactly one provider HTTP attempt. Retry and fallback
// policy belongs to internal/execution.
func doRequest(client *http.Client, request *http.Request) (*http.Response, error) {
	if err := providerauth.CheckRequestValidity(request); err != nil {
		return nil, err
	}
	// #nosec G704 -- Starport connectors call request-bound Starmap provider URLs.
	return client.Do(request)
}
