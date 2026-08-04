package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var hostnameRegex = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// isValidHostname checks if a string is a valid hostname
func isValidHostname(hostname string) bool {
	if len(hostname) > 253 {
		return false
	}
	return hostnameRegex.MatchString(hostname)
}

// Validate validates ServerConfig
func (c *ServerConfig) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port number: %d", c.Port)
	}

	if c.Host != "" {
		if ip := net.ParseIP(c.Host); ip == nil {
			// Not an IP, validate as hostname
			// Skip validation for common localhost values
			if c.Host != "0.0.0.0" && c.Host != "localhost" && c.Host != "127.0.0.1" {
				// Basic hostname validation without DNS lookup
				if !isValidHostname(c.Host) {
					return fmt.Errorf("invalid host: %s", c.Host)
				}
			}
		}
	}

	if c.ReadTimeout <= 0 {
		return fmt.Errorf("read timeout must be positive")
	}

	if c.WriteTimeout <= 0 {
		return fmt.Errorf("write timeout must be positive")
	}

	if c.IdleTimeout <= 0 {
		return fmt.Errorf("idle timeout must be positive")
	}
	if c.RequestTimeout < 0 {
		return fmt.Errorf("request timeout cannot be negative")
	}
	if c.MaxRequestSize < 0 {
		return fmt.Errorf("max request size cannot be negative")
	}

	if c.MaxHeaderBytes <= 0 {
		return fmt.Errorf("max header bytes must be positive")
	}

	return nil
}

// Validate validates StorageConfig
func (c *StorageConfig) Validate() error {
	switch c.Mode {
	case "badger":
		return c.Badger.Validate()
	case "valkey":
		return c.Valkey.Validate()
	default:
		return fmt.Errorf("unsupported storage mode: %s", c.Mode)
	}
}

// Validate validates Starmap catalog acquisition settings.
func (c *CatalogConfig) Validate() error {
	if c.RefreshInterval < 0 {
		return fmt.Errorf("catalog refresh interval cannot be negative")
	}
	if c.RefreshTimeout < 0 {
		return fmt.Errorf("catalog refresh timeout cannot be negative")
	}
	return nil
}

// Validate validates BadgerConfig
func (c *BadgerConfig) Validate() error {
	if c.Path == "" {
		return fmt.Errorf("badger path cannot be empty")
	}

	if c.Compression != "" && c.Compression != "none" && c.Compression != "snappy" && c.Compression != "zstd" {
		return fmt.Errorf("invalid compression type: %s", c.Compression)
	}

	if c.GCInterval <= 0 {
		return fmt.Errorf("GC interval must be positive")
	}

	if c.GCDiscardRatio < 0 || c.GCDiscardRatio > 1 {
		return fmt.Errorf("GC discard ratio must be between 0 and 1")
	}

	return nil
}

// Validate validates ValkeyConfig
func (c *ValkeyConfig) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("valkey URL cannot be empty")
	}

	u, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("invalid valkey URL: %w", err)
	}

	if u.Scheme != "valkey" && u.Scheme != "redis" && u.Scheme != "rediss" {
		return fmt.Errorf("invalid valkey URL scheme: %s", u.Scheme)
	}

	if c.MaxConnections <= 0 {
		return fmt.Errorf("max connections must be positive")
	}

	if c.MinIdleConns < 0 {
		return fmt.Errorf("min idle connections cannot be negative")
	}

	if c.MinIdleConns > c.MaxConnections {
		return fmt.Errorf("min idle connections cannot exceed max connections")
	}

	return nil
}

// Validate validates ProviderConfig
func (c *ProviderConfig) Validate() error {
	if c.BaseURL != "" {
		if _, err := url.Parse(c.BaseURL); err != nil {
			return fmt.Errorf("invalid base URL: %w", err)
		}
	}

	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	if c.MaxConnections <= 0 {
		return fmt.Errorf("max connections must be positive")
	}

	return nil
}

// Validate validates each active provider configuration.
func (c *ProvidersConfig) Validate() error {
	for _, entry := range c.Entries() {
		provider := entry.Config
		active := provider.APIKey != "" || provider.BaseURL != "" ||
			provider.ProjectID != "" || provider.Location != "" || provider.Enabled
		if !active {
			continue
		}
		if err := provider.Validate(); err != nil {
			return fmt.Errorf("invalid %s provider config: %w", entry.ProviderID, err)
		}
	}
	return nil
}

// Validate validates RateLimitingConfig
func (c *RateLimitingConfig) Validate() error {
	if c.GlobalRequestsPerSecond <= 0 {
		return fmt.Errorf("global requests per second must be positive")
	}

	if c.GlobalBurstMultiplier <= 0 {
		return fmt.Errorf("global burst multiplier must be positive")
	}

	if c.DefaultRequestsPerMinute <= 0 {
		return fmt.Errorf("default requests per minute must be positive")
	}

	if c.DefaultRequestsPerHour <= 0 {
		return fmt.Errorf("default requests per hour must be positive")
	}

	if c.DefaultTokensPerMinute <= 0 {
		return fmt.Errorf("default tokens per minute must be positive")
	}

	if c.DefaultTokensPerHour <= 0 {
		return fmt.Errorf("default tokens per hour must be positive")
	}

	if c.WindowSize <= 0 {
		return fmt.Errorf("window size must be positive")
	}

	if c.EnableHotReload && c.ConfigPath != "" {
		// Check if config path directory exists
		dir := c.ConfigPath[:strings.LastIndex(c.ConfigPath, "/")]
		if dir != "" && dir != "." {
			// Note: Directory might not exist during startup, which is okay
			// The hot reloader will handle this gracefully
			_, _ = os.Stat(dir)
		}
	}

	return nil
}

// Validate validates SecurityConfig
func (c *SecurityConfig) Validate() error {
	if c.BootstrapAPIKey != "" && len(c.BootstrapAPIKey) < 32 {
		return fmt.Errorf("bootstrap API key must be at least 32 characters")
	}
	if c.EnableTLS {
		if c.TLSCertPath == "" {
			return fmt.Errorf("TLS cert path required when TLS is enabled")
		}
		if c.TLSKeyPath == "" {
			return fmt.Errorf("TLS key path required when TLS is enabled")
		}

		// Check if cert and key files exist
		if _, err := os.Stat(c.TLSCertPath); os.IsNotExist(err) {
			return fmt.Errorf("TLS cert file not found: %s", c.TLSCertPath)
		}
		if _, err := os.Stat(c.TLSKeyPath); os.IsNotExist(err) {
			return fmt.Errorf("TLS key file not found: %s", c.TLSKeyPath)
		}
	}

	// Validate allowed origins format
	if c.AllowedOrigins != "*" {
		origins := strings.Split(c.AllowedOrigins, ",")
		for _, origin := range origins {
			origin = strings.TrimSpace(origin)
			if origin == "" {
				continue
			}
			if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
				return fmt.Errorf("invalid origin format: %s", origin)
			}
		}
	}

	return nil
}

// Validate validates LoggingConfig
func (c *LoggingConfig) Validate() error {
	validLevels := map[string]bool{
		"trace": true,
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
		"fatal": true,
		"panic": true,
	}

	if !validLevels[c.Level] {
		return fmt.Errorf("invalid log level: %s", c.Level)
	}

	if c.Format != "json" && c.Format != "text" {
		return fmt.Errorf("invalid log format: %s", c.Format)
	}

	if c.Output != "stdout" && c.Output != "stderr" && c.Output != "file" {
		return fmt.Errorf("invalid log output: %s", c.Output)
	}

	if c.Output == "file" && c.FilePath == "" {
		return fmt.Errorf("file path required when output is file")
	}

	if c.MaxSize <= 0 {
		return fmt.Errorf("max size must be positive")
	}

	if c.MaxBackups < 0 {
		return fmt.Errorf("max backups cannot be negative")
	}

	if c.MaxAge < 0 {
		return fmt.Errorf("max age cannot be negative")
	}

	return nil
}

// Validate validates ChatUIConfig
func (c *ChatUIConfig) Validate() error {
	// Validate theme
	if c.Theme != "light" && c.Theme != "dark" {
		return fmt.Errorf("invalid theme: %s (must be 'light' or 'dark')", c.Theme)
	}

	// Title cannot be empty
	if c.Title == "" {
		return fmt.Errorf("ChatUI title cannot be empty")
	}

	return nil
}
