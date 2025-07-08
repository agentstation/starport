package connectors

import (
	"io"
	"net/http"
)

// readResponseBody reads the response body and returns it as bytes
func readResponseBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}