package app

import (
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server"
)

// DefaultConfig provides sensible defaults for the application
var DefaultConfig = Config{
	Server: server.Config{
		Port: 8080,
		Host: "0.0.0.0",
	},
	StorageMode: "badger",
	LogLevel:    "info",
}

// Config holds application configuration
type Config struct {
	// Server configuration
	Server server.Config
	// Storage mode (badger or valkey)
	StorageMode string
	// Storage configuration
	Storage *config.StorageConfig
	// Log level
	LogLevel string
	// Providers configuration
	Providers *config.ProvidersConfig
	// Hot reload configuration
	HotReload *HotReloadConfig
	// Enable caching
	EnableCache bool
}

// HotReloadConfig holds hot reload settings
type HotReloadConfig struct {
	// Enable hot reload for rate limiting
	Enabled bool
	// Path to the rate limit config file
	ConfigPath string
	// Interval to check for config changes
	CheckInterval time.Duration
}

// Apply applies the given options to the config and returns a new config
func (c Config) Apply(opts ...Option) Config {
	// Create a copy to avoid modifying the original
	result := c

	// Apply all options
	for _, opt := range opts {
		opt(&result)
	}

	return result
}

// Option is a functional option for modifying Config
type Option func(*Config)

// WithServerConfig sets the server configuration
func WithServerConfig(serverCfg server.Config) Option {
	return func(cfg *Config) {
		cfg.Server = serverCfg
	}
}

// WithStorageMode sets the storage mode
func WithStorageMode(mode string) Option {
	return func(cfg *Config) {
		cfg.StorageMode = mode
	}
}

// WithLogLevel sets the log level
func WithLogLevel(level string) Option {
	return func(cfg *Config) {
		cfg.LogLevel = level
	}
}

// WithProvidersConfig sets the providers configuration
func WithProvidersConfig(providersCfg *config.ProvidersConfig) Option {
	return func(cfg *Config) {
		cfg.Providers = providersCfg
	}
}

// WithHotReload sets the hot reload configuration
func WithHotReload(hotReloadCfg *HotReloadConfig) Option {
	return func(cfg *Config) {
		cfg.HotReload = hotReloadCfg
	}
}

// WithStorageConfig sets the storage configuration
func WithStorageConfig(storageCfg *config.StorageConfig) Option {
	return func(cfg *Config) {
		cfg.Storage = storageCfg
	}
}

// WithCache enables caching
func WithCache(enable bool) Option {
	return func(cfg *Config) {
		cfg.EnableCache = enable
	}
}

// WithConfig sets the entire app configuration
func WithConfig(appCfg *Config) Option {
	return func(cfg *Config) {
		*cfg = *appCfg
	}
}
