package httpclient

import (
	"fmt"
	"time"
)

// Config defines the configuration options for creating an HTTP client
type Config struct {
	// Connection pool settings
	MaxIdleConns        int           // Maximum idle connections across all hosts
	MaxIdleConnsPerHost int           // Maximum idle connections per host
	MaxConnsPerHost     int           // Maximum total connections per host
	IdleConnTimeout     time.Duration // How long idle connections are kept alive

	// Timeout settings
	DialTimeout           time.Duration // Timeout for establishing connection
	TLSHandshakeTimeout   time.Duration // Timeout for TLS handshake
	ResponseHeaderTimeout time.Duration // Timeout for receiving response headers
	ExpectContinueTimeout time.Duration // Timeout for 100-continue response
	RequestTimeout        time.Duration // Overall timeout for entire request

	// Feature flags
	EnableHTTP2       bool // Enable HTTP/2 support
	EnableCompression bool // Enable transparent compression
	EnableKeepAlives  bool // Enable connection keep-alive

	// Monitoring and observability
	MetricsCollector MetricsCollector // Optional metrics collector

	// Circuit breaker configuration
	EnableCircuitBreaker bool                 // Enable circuit breaker
	CircuitBreakerConfig CircuitBreakerConfig // Circuit breaker settings

	// Advanced options
	TransportWrapper TransportWrapper // Optional transport wrapper for middleware
}

// CircuitBreakerConfig defines circuit breaker parameters
type CircuitBreakerConfig struct {
	FailureThreshold int           // Number of failures before opening
	SuccessThreshold int           // Number of successes before closing
	Timeout          time.Duration // Time before trying half-open state
}

// TransportWrapper allows wrapping the transport with middleware
type TransportWrapper func(transport RoundTripper) RoundTripper

// DefaultConfig returns a configuration optimized for LLM gateway usage
func DefaultConfig() Config {
	return Config{
		// Connection pool settings optimized for gateway
		MaxIdleConns:        500, // Total across all hosts
		MaxIdleConnsPerHost: 50,  // Per provider endpoint
		MaxConnsPerHost:     200, // Allow bursts per provider
		IdleConnTimeout:     90 * time.Second,

		// Timeouts suitable for LLM providers
		DialTimeout:           30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second, // LLMs can be slow to respond
		ExpectContinueTimeout: 1 * time.Second,
		RequestTimeout:        5 * time.Minute, // Long timeout for streaming

		// Features
		EnableHTTP2:       true, // Critical for multiplexing
		EnableCompression: true, // Let provider decide
		EnableKeepAlives:  true, // Reuse connections

		// Circuit breaker defaults
		EnableCircuitBreaker: true,
		CircuitBreakerConfig: CircuitBreakerConfig{
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          60 * time.Second,
		},

		// No metrics collector by default
		MetricsCollector: &NoOpMetricsCollector{},
	}
}

// DefaultProviderConfig returns provider-specific optimized configurations
func DefaultProviderConfig(provider string) Config {
	base := DefaultConfig()

	switch provider {
	case "openai":
		// OpenAI can handle more connections
		base.MaxConnsPerHost = 250
		base.MaxIdleConnsPerHost = 75
		base.ResponseHeaderTimeout = 90 * time.Second

	case "anthropic":
		// Anthropic has stricter rate limits
		base.MaxConnsPerHost = 100
		base.MaxIdleConnsPerHost = 30
		base.ResponseHeaderTimeout = 120 * time.Second // Claude can be slower

	case "google", "google-aistudio", "vertex-ai":
		// Google services support high concurrency
		base.MaxConnsPerHost = 300
		base.MaxIdleConnsPerHost = 100
		base.EnableHTTP2 = true // Ensure HTTP/2 is on

	case "groq":
		// Groq is optimized for speed
		base.MaxConnsPerHost = 150
		base.MaxIdleConnsPerHost = 50
		base.ResponseHeaderTimeout = 30 * time.Second

	case "azure":
		// Azure OpenAI service
		base.MaxConnsPerHost = 200
		base.MaxIdleConnsPerHost = 60
		base.TLSHandshakeTimeout = 15 * time.Second // Azure can be slower
	}

	return base
}

// Validate checks if the configuration is valid
func (c Config) Validate() error {
	if c.MaxIdleConns <= 0 {
		return fmt.Errorf("MaxIdleConns must be positive")
	}
	if c.MaxIdleConnsPerHost <= 0 {
		return fmt.Errorf("MaxIdleConnsPerHost must be positive")
	}
	if c.MaxConnsPerHost <= 0 {
		return fmt.Errorf("MaxConnsPerHost must be positive")
	}
	if c.MaxIdleConnsPerHost > c.MaxConnsPerHost {
		return fmt.Errorf("MaxIdleConnsPerHost cannot exceed MaxConnsPerHost")
	}
	if c.IdleConnTimeout <= 0 {
		return fmt.Errorf("IdleConnTimeout must be positive")
	}
	if c.DialTimeout <= 0 {
		return fmt.Errorf("DialTimeout must be positive")
	}
	if c.TLSHandshakeTimeout <= 0 {
		return fmt.Errorf("TLSHandshakeTimeout must be positive")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("RequestTimeout must be positive")
	}

	return nil
}
