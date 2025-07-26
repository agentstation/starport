// Package proxy provides a high-performance LLM request proxy with support for
// multiple providers, intelligent routing, caching, and extensible middleware.
package proxy

import (
	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/routing"
)

// Config holds the configuration for creating a new proxy service.
type Config struct {
	// Registry provides access to LLM provider connectors
	Registry *registry.Registry
	
	// Router handles intelligent model selection and failover
	Router routing.ModelRouter
	
	// CacheManager handles response caching (optional)
	CacheManager *cache.Manager
	
	// CacheConfig configures caching behavior (optional)
	CacheConfig *CacheConfig
	
	// Middlewares to apply to the proxy service
	Middlewares []Middleware
}

// Option configures the proxy service.
type Option func(*Config)

// WithCache enables caching with the specified cache manager and configuration.
func WithCache(manager *cache.Manager, config *CacheConfig) Option {
	return func(c *Config) {
		c.CacheManager = manager
		c.CacheConfig = config
	}
}

// WithCacheConfig sets custom cache configuration.
// If a cache manager is not provided separately, a default one will be created.
func WithCacheConfig(config *CacheConfig) Option {
	return func(c *Config) {
		c.CacheConfig = config
	}
}

// WithMiddleware adds a middleware to the proxy service.
// Middlewares are applied in the order they are added.
func WithMiddleware(m Middleware) Option {
	return func(c *Config) {
		c.Middlewares = append(c.Middlewares, m)
	}
}

// New creates a new proxy service with the given registry and router.
// Additional functionality can be added using options.
//
// Example:
//
//	// Basic proxy
//	proxy := proxy.New(registry, router)
//	
//	// Proxy with caching
//	proxy := proxy.New(registry, router,
//	    proxy.WithCache(cacheManager, cacheConfig),
//	)
//	
//	// Proxy with custom middleware
//	proxy := proxy.New(registry, router,
//	    proxy.WithMiddleware(loggingMiddleware),
//	    proxy.WithMiddleware(metricsMiddleware),
//	)
func New(registry *registry.Registry, router routing.ModelRouter, opts ...Option) Service {
	// Initialize config with required dependencies
	cfg := &Config{
		Registry: registry,
		Router:   router,
	}
	
	// Apply options
	for _, opt := range opts {
		opt(cfg)
	}
	
	// Create the core proxy implementation
	core := &proxyImpl{
		registry: cfg.Registry,
		router:   cfg.Router,
	}
	
	// Build the service with middleware chain
	var service Service = core
	
	// Apply custom middlewares in reverse order so the first middleware
	// added is the outermost (called first)
	for i := len(cfg.Middlewares) - 1; i >= 0; i-- {
		service = cfg.Middlewares[i].Wrap(service)
	}
	
	// Add cache middleware if configured
	if cfg.CacheManager != nil && cfg.CacheConfig != nil {
		service = NewCachedService(service, cfg.CacheManager, *cfg.CacheConfig)
	}
	
	return service
}

// NewFromConfig creates a new proxy service from a configuration struct.
// This is useful when you have a pre-built configuration.
func NewFromConfig(config *Config) Service {
	if config.Registry == nil || config.Router == nil {
		panic("proxy: Registry and Router are required")
	}
	
	return New(config.Registry, config.Router,
		WithCache(config.CacheManager, config.CacheConfig),
		func(c *Config) {
			c.Middlewares = config.Middlewares
		},
	)
}

// NewProxy is an alias for New to provide a more explicit name.
// 
// Example:
//
//	proxy := proxy.NewProxy(registry, router)
func NewProxy(registry *registry.Registry, router routing.ModelRouter, opts ...Option) Service {
	return New(registry, router, opts...)
}