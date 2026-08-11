package config

import "github.com/agentstation/starport/internal/storage"

const (
	storageModeBadger = storage.StorageTypeBadger
	compressionNone   = "none"
)

// RuntimeStorage projects external storage settings into the storage adapter contract.
func (c StorageConfig) RuntimeStorage() storage.Config {
	return storage.Config{
		Type: c.Mode,
		Badger: storage.BadgerConfig{
			Path: c.Badger.Path, InMemory: c.Badger.inMemory,
			SyncWrites:  c.Badger.SyncWrites,
			Compression: c.Badger.Compression != compressionNone, NumVersions: 1,
			NumLevelZero: 5, MemTableSize: 64 << 20,
		},
		Valkey: storage.ValkeyConfig{
			URL: c.Valkey.URL, Password: c.Valkey.Password,
			MaxRetries: 3, MinIdleConns: c.Valkey.MinIdleConns,
			ReadTimeout: c.Valkey.ReadTimeout, WriteTimeout: c.Valkey.WriteTimeout,
			ClusterMode: c.Valkey.ClusterMode,
		},
	}
}

// ConfigureDevelopmentRuntime selects process-local settings that cannot expose
// a development gateway or create persistent state.
func (c *Config) ConfigureDevelopmentRuntime() {
	if c == nil {
		return
	}
	c.Server.Host = "127.0.0.1"
	c.Server.EnableProfiling = false
	c.Catalog.RefreshOnStart = false
	c.Catalog.RefreshInterval = 0
	c.Storage = StorageConfig{
		Mode: storageModeBadger,
		Badger: BadgerConfig{
			Compression: compressionNone, inMemory: true,
		},
	}
	c.Security.MasterKey = ""
	c.Security.EnableTLS = false
	c.Security.TLSCertPath = ""
	c.Security.TLSKeyPath = ""
	c.Security.EnableCORS = false
	c.Security.AllowedOrigins = ""
	c.Security.JWTSecret = ""
	c.Logging.Output = "stdout"
	c.Logging.FilePath = ""
	c.RateLimiting.EnableHotReload = false
}
