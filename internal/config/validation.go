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
	case storageModeBadger:
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
	if c.RemoteActivationInterval < 0 {
		return fmt.Errorf("catalog remote activation interval cannot be negative")
	}
	if c.RemoteURL == "" {
		if c.RemoteAPIKey != "" {
			return fmt.Errorf("catalog remote API key requires a remote URL")
		}
		return nil
	}
	if c.RemoteActivationInterval == 0 {
		return fmt.Errorf("catalog remote activation interval must be positive")
	}
	if c.WorkspacePath != "" {
		return fmt.Errorf("catalog remote URL and workspace path are mutually exclusive")
	}
	if c.RefreshOnStart {
		return fmt.Errorf("catalog remote URL and refresh on start are mutually exclusive")
	}
	if c.RefreshInterval != 0 {
		return fmt.Errorf("catalog remote URL and local refresh interval are mutually exclusive")
	}
	return nil
}

// Validate validates the direct inference secret-source lifecycle.
func (c *CredentialSourcesConfig) Validate() error {
	if c.RemoteRefreshInterval < 0 {
		return fmt.Errorf("credential source remote refresh interval cannot be negative")
	}
	if c.ReconcileInterval < 0 {
		return fmt.Errorf("credential source reconcile interval cannot be negative")
	}
	if c.ReconcileTimeout < 0 {
		return fmt.Errorf("credential source reconcile timeout cannot be negative")
	}
	if c.ReconcileInterval > 0 && c.ReconcileTimeout == 0 {
		return fmt.Errorf("credential source reconcile timeout must be positive when reconciliation is enabled")
	}
	return nil
}

// Validate validates BadgerConfig
func (c *BadgerConfig) Validate() error {
	if !c.inMemory && c.Path == "" {
		return fmt.Errorf("badger path cannot be empty")
	}
	if c.inMemory && c.Path != "" {
		return fmt.Errorf("in-memory badger cannot use a filesystem path")
	}

	if c.Compression != "" && c.Compression != compressionNone && c.Compression != "snappy" && c.Compression != "zstd" {
		return fmt.Errorf("invalid compression type: %s", c.Compression)
	}
	if c.inMemory {
		return nil
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
		return fmt.Errorf("valkey URL is invalid")
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

// Validate validates ProviderConfig.
func (c *ProviderConfig) Validate() error {
	if c.BaseURL != "" {
		if _, err := url.Parse(c.BaseURL); err != nil {
			return fmt.Errorf("base URL is invalid")
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
		active := !provider.Material.Empty() || provider.CredentialSource != nil ||
			provider.BaseURL != "" || len(provider.CredentialReferences) > 0 || provider.Enabled
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
	if c.DefaultRequestsPerMinute <= 0 {
		return fmt.Errorf("default requests per minute must be positive")
	}

	if c.WindowSize <= 0 {
		return fmt.Errorf("window size must be positive")
	}

	return nil
}

// Validate validates SecurityConfig
func (c *SecurityConfig) Validate() error {
	if c.MasterKey != "" && len(c.MasterKey) < 32 {
		return fmt.Errorf("master key must be at least 32 bytes")
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
