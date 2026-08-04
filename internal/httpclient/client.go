package httpclient

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Client represents an HTTP client optimized for a specific provider
type Client struct {
	httpClient *http.Client
	transport  *MonitoredTransport
	config     Config
	provider   string
	mu         sync.RWMutex
}

// New creates a new HTTP client for the specified provider
func New(provider string, config Config) (*Client, error) {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create base transport with optimized settings
	baseTransport := &http.Transport{
		// Connection pool configuration
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConnsPerHost,
		MaxConnsPerHost:     config.MaxConnsPerHost,
		IdleConnTimeout:     config.IdleConnTimeout,

		// Timeouts
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		ExpectContinueTimeout: config.ExpectContinueTimeout,

		// Features
		ForceAttemptHTTP2:  config.EnableHTTP2,
		DisableCompression: !config.EnableCompression,
		DisableKeepAlives:  !config.EnableKeepAlives,

		// Custom dialer with timeout
		DialContext: (&net.Dialer{
			Timeout:   config.DialTimeout,
			KeepAlive: 30 * time.Second,
			DualStack: true, // Try both IPv4 and IPv6
		}).DialContext,

		// Proxy from environment
		Proxy: http.ProxyFromEnvironment,
	}

	monitoredTransport := &MonitoredTransport{
		base:     baseTransport,
		provider: provider,
		metrics:  config.MetricsCollector,
	}

	// Apply transport wrapper if provided
	var finalTransport RoundTripper = monitoredTransport
	if config.TransportWrapper != nil {
		finalTransport = config.TransportWrapper(finalTransport)
	}

	// Create HTTP client
	httpClient := &http.Client{
		Transport: finalTransport,
		Timeout:   config.RequestTimeout,
		// Don't follow redirects automatically for API calls
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &Client{
		httpClient: httpClient,
		transport:  monitoredTransport,
		config:     config,
		provider:   provider,
	}, nil
}

// Do executes an HTTP request
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	// #nosec G704 -- this client executes requests built by provider connectors using configured provider endpoints.
	return c.httpClient.Do(req)
}

// GetHTTPClient returns the underlying HTTP client
func (c *Client) GetHTTPClient() *http.Client {
	return c.httpClient
}

// Provider returns the provider name this client is configured for
func (c *Client) Provider() string {
	return c.provider
}

// Stats returns current connection pool statistics
func (c *Client) Stats() ConnectionStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Get transport stats if available
	transport, ok := c.httpClient.Transport.(*MonitoredTransport)
	if !ok {
		// If wrapped, try to unwrap
		if wrapper, ok := c.httpClient.Transport.(interface{ Unwrap() RoundTripper }); ok {
			transport, _ = wrapper.Unwrap().(*MonitoredTransport)
		}
	}

	if !ok || transport == nil {
		return ConnectionStats{Provider: c.provider}
	}

	// Get base transport
	_, ok = transport.base.(*http.Transport)
	if !ok {
		return ConnectionStats{Provider: c.provider}
	}

	// Note: These methods are not exposed in the standard library
	// In practice, you'd need to track these metrics separately
	// or use runtime reflection (not recommended for production)
	stats := ConnectionStats{
		Provider: c.provider,
		// In a real implementation, you'd track these metrics
		// through the MonitoredTransport
		IdleConnections:   -1, // Not directly accessible
		ActiveConnections: -1, // Not directly accessible
		TotalConnections:  -1, // Not directly accessible
	}

	// Record current stats
	c.config.MetricsCollector.RecordPoolStats(c.provider, stats)

	return stats
}

// Close closes all idle connections
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Close idle connections
	if transport, ok := c.httpClient.Transport.(*MonitoredTransport); ok {
		if baseTransport, ok := transport.base.(*http.Transport); ok {
			baseTransport.CloseIdleConnections()
		}
	}
}

// UpdateConfig updates the client configuration
// Note: This creates a new transport, so use sparingly
func (c *Client) UpdateConfig(config Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Validate new configuration
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Create new client with updated config
	newClient, err := New(c.provider, config)
	if err != nil {
		return err
	}

	// Close old connections
	if oldTransport, ok := c.httpClient.Transport.(*MonitoredTransport); ok {
		if baseTransport, ok := oldTransport.base.(*http.Transport); ok {
			baseTransport.CloseIdleConnections()
		}
	}

	// Swap in new client
	c.httpClient = newClient.httpClient
	c.transport = newClient.transport
	c.config = config

	return nil
}
