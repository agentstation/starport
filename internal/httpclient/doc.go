// Package httpclient provides optimized HTTP clients for LLM providers with
// built-in monitoring and connection pooling.
//
// This package is designed specifically for high-throughput LLM gateway scenarios
// where multiple providers need to be accessed concurrently with different
// performance characteristics.
//
// Features:
//   - Optimized connection pooling per provider
//   - HTTP/2 support with multiplexing
//   - Comprehensive metrics collection
//   - Middleware support for rate limiting, request IDs, and timeouts
//   - Provider-specific configuration defaults
//
// Basic Usage:
//
//	// Create a client with default configuration
//	client, err := httpclient.New("openai", httpclient.DefaultConfig())
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	// Use the client
//	resp, err := client.Do(req)
//
// With Monitoring:
//
//	// Implement MetricsCollector interface
//	metrics := &MyMetricsCollector{}
//
//	config := httpclient.DefaultConfig()
//	config.MetricsCollector = metrics
//
//	client, err := httpclient.New("anthropic", config)
//
// With Middleware:
//
//	config := httpclient.DefaultConfig()
//	config.TransportWrapper = httpclient.ChainTransportWrappers(
//	    httpclient.WithRateLimiting(100),
//	    httpclient.WithRequestID(nil),
//	)
//
//	client, err := httpclient.New("google", config)
//
// The package is designed to be used as a foundation for LLM provider
// connectors, handling all the complexity of HTTP communication while
// allowing the connectors to focus on provider-specific logic.
package httpclient
