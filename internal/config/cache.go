package config

import (
	"errors"

	"github.com/agentstation/starport/internal/cache/connection"
)

const cacheBackendLocal = "local"

// Validate selects local memory or one dedicated shared-cache service.
func (c *CacheConfig) Validate(durableURL string) error {
	switch c.Backend {
	case "", cacheBackendLocal:
		if c.URL != "" || c.Namespace != "" || c.AllowInsecure {
			return errors.New("local cache cannot configure a shared endpoint")
		}
	case "valkey":
		if _, err := connection.Parse(c.URL, c.Namespace, c.AllowInsecure); err != nil {
			return err
		}
		if durableURL != "" && connection.SameServer(c.URL, durableURL) {
			return errors.New("cache requires a separate service from durable KV; another database does not isolate eviction")
		}
	default:
		return errors.New("cache backend must be local or valkey")
	}
	return nil
}
