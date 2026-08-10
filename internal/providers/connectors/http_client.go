package connectors

import (
	"fmt"
	"net/http"

	"github.com/agentstation/starport/internal/httpclient"
	"github.com/agentstation/starport/internal/providerauth"
)

func newProviderHTTPClient(
	provider string,
	config ProviderConfig,
	bearerSources ...providerauth.Source,
) (*http.Client, error) {
	if len(bearerSources) > 1 {
		return nil, fmt.Errorf("provider HTTP client accepts at most one bearer credential source")
	}
	clientConfig := httpclient.DefaultConfig()
	// ProviderConfig.Timeout is treated as a first-byte/header timeout. Do not
	// map it directly to http.Client.Timeout because that deadline also covers
	// response-body reads and cuts off healthy long-running streams.
	clientConfig.ResponseHeaderTimeout = config.Timeout
	if config.Timeout > clientConfig.RequestTimeout {
		clientConfig.RequestTimeout = config.Timeout
	}

	if config.MaxConnections > 0 {
		clientConfig.MaxConnsPerHost = config.MaxConnections
		clientConfig.MaxIdleConnsPerHost = config.MaxConnections
		clientConfig.MaxIdleConns = config.MaxConnections
	}
	if len(bearerSources) == 1 {
		source := bearerSources[0]
		clientConfig.TransportWrapper = func(base httpclient.RoundTripper) httpclient.RoundTripper {
			return providerauth.NewBearerTransport(base, source)
		}
	}

	client, err := httpclient.New(provider, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return client.GetHTTPClient(), nil
}
