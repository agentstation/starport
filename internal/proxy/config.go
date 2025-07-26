package proxy

import (
	"time"
	
	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/routing"
)

// DefaultConfig returns a default proxy configuration.
func DefaultConfig() *Config {
	return &Config{
		Middlewares: []Middleware{},
	}
}

// Options contains advanced configuration options for the proxy.
type Options struct {
	// RequestTimeout is the timeout for individual requests
	RequestTimeout time.Duration
	
	// EnableMetrics enables metrics collection
	EnableMetrics bool
	
	// EnableLogging enables request/response logging
	EnableLogging bool
	
	// MaxRetries is the maximum number of retry attempts
	MaxRetries int
	
	// RetryDelay is the delay between retry attempts
	RetryDelay time.Duration
}

// DefaultOptions returns default proxy options.
func DefaultOptions() *Options {
	return &Options{
		RequestTimeout: 30 * time.Second,
		EnableMetrics:  true,
		EnableLogging:  true,
		MaxRetries:     3,
		RetryDelay:     100 * time.Millisecond,
	}
}

// WithOptions sets advanced proxy options.
func WithOptions(opts *Options) Option {
	return func(c *Config) {
		// In the future, these options can be used to configure
		// built-in middleware or proxy behavior
		if opts.EnableLogging {
			// Add logging middleware
			c.Middlewares = append(c.Middlewares, LoggingMiddleware())
		}
		// TODO: Add metrics middleware when implemented
		// if opts.EnableMetrics {
		//     c.Middlewares = append(c.Middlewares, MetricsMiddleware())
		// }
		_ = opts.EnableMetrics // Mark as intentionally unused for now
	}
}

// WithRequestTimeout sets the timeout for individual requests.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(_ *Config) {
		// This will be used when we implement timeout middleware
		// For now, we can store it in the config for future use
		_ = timeout // Mark as intentionally unused for now
	}
}

// ValidationConfig configures request validation behavior.
type ValidationConfig struct {
	// StrictMode enables strict validation of requests
	StrictMode bool
	
	// MaxTokensLimit is the maximum allowed max_tokens value
	MaxTokensLimit int
	
	// MaxMessagesLimit is the maximum number of messages allowed
	MaxMessagesLimit int
	
	// MaxMessageLength is the maximum length of a single message
	MaxMessageLength int
}

// DefaultValidationConfig returns default validation configuration.
func DefaultValidationConfig() *ValidationConfig {
	return &ValidationConfig{
		StrictMode:       false,
		MaxTokensLimit:   1000000, // 1M tokens
		MaxMessagesLimit: 1000,
		MaxMessageLength: 1000000, // 1M characters
	}
}

// WithValidation configures request validation.
func WithValidation(config *ValidationConfig) Option {
	return func(_ *Config) {
		// Store validation config for use in validation middleware
		// This will be implemented when we create the validation middleware
		_ = config // Mark as intentionally unused for now
	}
}

// RoutingConfig configures routing behavior.
type RoutingConfig struct {
	// EnableFailover enables automatic failover to other providers
	EnableFailover bool
	
	// PreferredProviders is an ordered list of preferred providers
	PreferredProviders []string
	
	// ExcludedProviders is a list of providers to exclude
	ExcludedProviders []string
	
	// EnableLoadBalancing enables load balancing across providers
	EnableLoadBalancing bool
	
	// EnableStickyRouting enables sticky routing for conversations
	EnableStickyRouting bool
}

// DefaultRoutingConfig returns default routing configuration.
func DefaultRoutingConfig() *RoutingConfig {
	return &RoutingConfig{
		EnableFailover:      true,
		EnableLoadBalancing: false,
		EnableStickyRouting: false,
	}
}

// WithRouting configures routing behavior.
func WithRouting(config *RoutingConfig) Option {
	return func(_ *Config) {
		// This configuration will be passed to the router
		// when we implement configurable routing
		_ = config // Mark as intentionally unused for now
	}
}

// SecurityConfig configures security features.
type SecurityConfig struct {
	// EnableRateLimiting enables rate limiting
	EnableRateLimiting bool
	
	// RateLimitPerMinute is the number of requests allowed per minute
	RateLimitPerMinute int
	
	// EnableContentFiltering enables content filtering
	EnableContentFiltering bool
	
	// BlockedPatterns is a list of regex patterns to block
	BlockedPatterns []string
	
	// EnableAPIKeyValidation enables API key validation
	EnableAPIKeyValidation bool
}

// DefaultSecurityConfig returns default security configuration.
func DefaultSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		EnableRateLimiting:     true,
		RateLimitPerMinute:     60,
		EnableContentFiltering: false,
		EnableAPIKeyValidation: true,
	}
}

// WithSecurity configures security features.
func WithSecurity(config *SecurityConfig) Option {
	return func(_ *Config) {
		// Security middleware will use this configuration
		// when implemented
		_ = config // Mark as intentionally unused for now
	}
}

// Builder provides a fluent interface for building proxy configuration.
type Builder struct {
	config *Config
}

// NewBuilder creates a new proxy configuration builder.
func NewBuilder(registry *registry.Registry, router routing.ModelRouter) *Builder {
	return &Builder{
		config: &Config{
			Registry:    registry,
			Router:      router,
			Middlewares: []Middleware{},
		},
	}
}

// WithCache adds caching to the proxy.
func (b *Builder) WithCache(manager *cache.Manager, config *CacheConfig) *Builder {
	b.config.CacheManager = manager
	b.config.CacheConfig = config
	return b
}

// WithMiddleware adds a middleware to the proxy.
func (b *Builder) WithMiddleware(m Middleware) *Builder {
	b.config.Middlewares = append(b.config.Middlewares, m)
	return b
}

// WithOptions adds advanced options to the proxy.
func (b *Builder) WithOptions(opts *Options) *Builder {
	WithOptions(opts)(b.config)
	return b
}

// Build creates the proxy service with the configured options.
func (b *Builder) Build() Service {
	return NewFromConfig(b.config)
}