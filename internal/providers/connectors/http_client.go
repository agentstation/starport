package connectors

import (
	"fmt"
	"net/http"

	"github.com/agentstation/starport/internal/httpclient"
)

func newProviderHTTPClient(provider string, config ProviderConfig) (*http.Client, error) {
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

	client, err := httpclient.New(provider, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return client.GetHTTPClient(), nil
}
