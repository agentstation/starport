package connectors

import (
	"net"
	"net/http"
	"time"
)

const (
	providerDialTimeout           = 30 * time.Second
	providerDialKeepAlive         = 30 * time.Second
	providerIdleConnectionTimeout = 90 * time.Second
	providerTLSHandshakeTimeout   = 10 * time.Second
	providerExpectContinueTimeout = time.Second
)

// newProviderHTTPClient owns connection and first-response-byte policy for
// provider connectors. Execution contexts own the total request deadline.
func newProviderHTTPClient(config ProviderConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = config.MaxConnections
	transport.MaxIdleConnsPerHost = config.MaxConnections
	transport.MaxConnsPerHost = config.MaxConnections
	transport.IdleConnTimeout = providerIdleConnectionTimeout
	transport.TLSHandshakeTimeout = providerTLSHandshakeTimeout
	transport.ResponseHeaderTimeout = config.Timeout
	transport.ExpectContinueTimeout = providerExpectContinueTimeout
	transport.ForceAttemptHTTP2 = true
	transport.DialContext = (&net.Dialer{
		Timeout:   providerDialTimeout,
		KeepAlive: providerDialKeepAlive,
	}).DialContext

	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
