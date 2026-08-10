package providerauth

import (
	"fmt"
	"net/http"
)

// NewBearerTransport returns a transport that obtains a bearer token for each
// request. The source can cache a fresh token between requests.
func NewBearerTransport(base http.RoundTripper, source Source) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &bearerTransport{base: base, source: source}
}

type bearerTransport struct {
	base   http.RoundTripper
	source Source
}

func (t *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.source == nil {
		closeRequestBody(request)
		return nil, ErrSourceRequired
	}
	token, err := t.source.Token(request.Context())
	if err != nil {
		closeRequestBody(request)
		return nil, fmt.Errorf("authorize provider inference request: %w", err)
	}

	authorized := request.Clone(request.Context())
	authorized.Header = request.Header.Clone()
	authorized.Header.Set("Authorization", "Bearer "+token.Value)
	return t.base.RoundTrip(authorized)
}

func closeRequestBody(request *http.Request) {
	if request != nil && request.Body != nil {
		_ = request.Body.Close()
	}
}

// CloseIdleConnections closes idle connections in the wrapped transport when
// it supports that operation.
func (t *bearerTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
