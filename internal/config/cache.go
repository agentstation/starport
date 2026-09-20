package config

import (
	"errors"

	"github.com/agentstation/starport/internal/cache/connection"
)

const cacheBackendLocal = "local"
const cacheCAFileEnvironment = "STARPORT_CACHE_CA_FILE"
const cacheCAFileRole = "cache-ca"

// Validate selects local memory or one dedicated shared-cache service.
func (c *CacheConfig) Validate(durableURL string) error {
	switch c.Backend {
	case "", cacheBackendLocal:
		if c.URL != "" || c.AllowInsecure || c.CAFile != "" {
			return errors.New("local cache cannot configure a shared endpoint")
		}
	case "valkey":
		u, err := connection.Parse(c.URL, c.AllowInsecure)
		if err != nil {
			return err
		}
		if c.CAFile != "" && u.Scheme != "valkeys" && u.Scheme != "rediss" {
			return errors.New("cache CA file requires a TLS endpoint")
		}
		if durableURL != "" && connection.SameServer(c.URL, durableURL) {
			return errors.New("cache requires a separate service from durable KV; another database does not isolate eviction")
		}
	default:
		return errors.New("cache backend must be local or valkey")
	}
	return nil
}
