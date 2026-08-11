// Package config provides configuration management for Starport.
// It supports loading configuration from environment variables and .env files,
// with comprehensive validation and hot reload capabilities for rate limiting rules.
package config

import (
	"sort"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// Config represents the complete application configuration
type Config struct {
	Server            ServerConfig            `env:",prefix=SERVER_"`
	Storage           StorageConfig           `env:",prefix=STORAGE_"`
	Catalog           CatalogConfig           `env:",prefix=CATALOG_"`
	CredentialSources CredentialSourcesConfig `env:",prefix=CREDENTIAL_SOURCES_"`
	Providers         ProvidersConfig
	RateLimiting      RateLimitingConfig `env:",prefix=RATE_LIMITING_"`
	Security          SecurityConfig     `env:",prefix=SECURITY_"`
	Logging           LoggingConfig      `env:",prefix=LOGGING_"`
	Cache             CacheConfig        `env:",prefix=CACHE_"`
	ChatUI            ChatUIConfig       `env:",prefix=CHATUI_"`

	providerEnvironment environmentLookup
	credentialResolver  *credentials.Resolver
}

// CredentialSourcesConfig defines direct inference secret-source lifecycle.
type CredentialSourcesConfig struct {
	RemoteRefreshInterval time.Duration `env:"REMOTE_REFRESH_INTERVAL,default=5m"`
}

// CatalogConfig defines Starmap acquisition and tenant workspace settings.
// Acquisition credentials remain in Starmap's provider environment contract.
type CatalogConfig struct {
	WorkspacePath   string        `env:"WORKSPACE_PATH"`
	RefreshOnStart  bool          `env:"REFRESH_ON_START,default=false"`
	RefreshInterval time.Duration `env:"REFRESH_INTERVAL,default=0s"`
	RefreshTimeout  time.Duration `env:"REFRESH_TIMEOUT,default=2m"`
}

// ServerConfig defines HTTP server settings
type ServerConfig struct {
	Port              int           `env:"PORT,default=8080"`
	Host              string        `env:"HOST,default=127.0.0.1"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT,default=30s"`
	WriteTimeout      time.Duration `env:"WRITE_TIMEOUT,default=30s"`
	IdleTimeout       time.Duration `env:"IDLE_TIMEOUT,default=120s"`
	RequestTimeout    time.Duration `env:"REQUEST_TIMEOUT,default=60s"`
	MaxRequestSize    int64         `env:"MAX_REQUEST_SIZE,default=10485760"`
	MaxHeaderBytes    int           `env:"MAX_HEADER_BYTES,default=1048576"`
	ShutdownTimeout   time.Duration `env:"SHUTDOWN_TIMEOUT,default=30s"`
	EnableProfiling   bool          `env:"ENABLE_PROFILING,default=false"`
	EnableHealthCheck bool          `env:"ENABLE_HEALTH_CHECK,default=true"`
}

// StorageConfig defines storage backend settings
type StorageConfig struct {
	Mode   string       `env:"MODE,default=badger"`
	Badger BadgerConfig `env:",prefix=BADGER_"`
	Valkey ValkeyConfig `env:",prefix=VALKEY_"`
}

// BadgerConfig defines Badger DB settings
type BadgerConfig struct {
	Path           string        `env:"PATH,overwrite"`
	SyncWrites     bool          `env:"SYNC_WRITES,default=false"`
	Compression    string        `env:"COMPRESSION,default=snappy"`
	GCInterval     time.Duration `env:"GC_INTERVAL,default=5m"`
	GCDiscardRatio float64       `env:"GC_DISCARD_RATIO,default=0.5"`
}

// ValkeyConfig defines Valkey/Redis settings
type ValkeyConfig struct {
	URL            string        `env:"URL,default=valkey://localhost:6379" redact:"url"`
	MaxConnections int           `env:"MAX_CONNECTIONS,default=50"`
	MinIdleConns   int           `env:"MIN_IDLE_CONNS,default=10"`
	DialTimeout    time.Duration `env:"DIAL_TIMEOUT,default=5s"`
	ReadTimeout    time.Duration `env:"READ_TIMEOUT,default=3s"`
	WriteTimeout   time.Duration `env:"WRITE_TIMEOUT,default=3s"`
	IdleTimeout    time.Duration `env:"IDLE_TIMEOUT,default=5m"`
	ClusterMode    bool          `env:"CLUSTER_MODE,default=false"`
	Password       string        `env:"PASSWORD" secret:"true"`
}

// ProvidersConfig stores inference settings by exact Starmap provider ID.
// Provider membership comes from the active catalog, not this map.
type ProvidersConfig map[catalogs.ProviderID]ProviderConfig

// ProviderConfig defines settings for a single LLM provider
type ProviderConfig struct {
	BaseURL              string                                                     `redact:"url"`
	CredentialReferences map[catalogs.ProviderCredentialFieldID]CredentialReference `json:"credential_references,omitempty"`
	Material             credentials.Material                                       `json:"-"`
	CredentialSource     credentials.MaterialSource                                 `json:"-"`
	Timeout              time.Duration                                              `json:"timeout"`
	MaxConnections       int                                                        `json:"max_connections"`
	Enabled              bool                                                       `json:"enabled"`
	EndpointBindings     map[string]string                                          `json:"endpoint_bindings,omitempty"`
}

// CredentialReference selects one explicit source for a catalog credential
// field. Ambient fallback applies only to a not-configured source result.
type CredentialReference struct {
	Reference       string `json:"reference"`
	FallbackAmbient bool   `json:"fallback_ambient,omitempty"`
}

// ProviderEntry binds external operator configuration to one exact Starmap provider ID.
type ProviderEntry struct {
	ProviderID catalogs.ProviderID
	Config     ProviderConfig
}

// CloneProvidersConfig returns a caller-owned copy of deployment provider
// settings. Material and source values are immutable handles.
func CloneProvidersConfig(source ProvidersConfig) ProvidersConfig {
	if source == nil {
		return nil
	}
	result := make(ProvidersConfig, len(source))
	for providerID, provider := range source {
		provider.CredentialReferences = cloneCredentialReferences(provider.CredentialReferences)
		provider.EndpointBindings = cloneProviderStrings(provider.EndpointBindings)
		result[providerID] = provider
	}
	return result
}

func cloneProviderStrings(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// Entries returns all supported external configuration slots. Adapter
// semantics and provider membership remain outside the configuration package.
func (c ProvidersConfig) Entries() []ProviderEntry {
	providerIDs := make([]catalogs.ProviderID, 0, len(c))
	for providerID := range c {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Slice(providerIDs, func(left, right int) bool { return providerIDs[left] < providerIDs[right] })
	entries := make([]ProviderEntry, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		entries = append(entries, ProviderEntry{ProviderID: providerID, Config: c[providerID]})
	}
	return entries
}

// EnableProvider marks one exact catalog provider for activation. Catalog
// resolution supplies its defaults and endpoint bindings later.
func (c *Config) EnableProvider(providerID catalogs.ProviderID) {
	if c.Providers == nil {
		c.Providers = make(ProvidersConfig)
	}
	provider := c.Providers[providerID]
	provider.Enabled = true
	c.Providers[providerID] = provider
}

// RateLimitingConfig defines rate limiting settings
type RateLimitingConfig struct {
	// Global limits
	GlobalRequestsPerSecond int     `env:"GLOBAL_REQUESTS_PER_SECOND,default=10000"`
	GlobalBurstMultiplier   float64 `env:"GLOBAL_BURST_MULTIPLIER,default=2.0"`

	// Default key limits
	DefaultRequestsPerMinute int `env:"DEFAULT_REQUESTS_PER_MINUTE,default=60"`
	DefaultRequestsPerHour   int `env:"DEFAULT_REQUESTS_PER_HOUR,default=1000"`
	DefaultTokensPerMinute   int `env:"DEFAULT_TOKENS_PER_MINUTE,default=100000"`
	DefaultTokensPerHour     int `env:"DEFAULT_TOKENS_PER_HOUR,default=1000000"`
	DefaultBurst             int `env:"DEFAULT_BURST,default=10"`

	// Rate limit window
	WindowSize      time.Duration `env:"WINDOW_SIZE,default=1m"`
	SyncInterval    time.Duration `env:"SYNC_INTERVAL,default=5s"`
	CleanupInterval time.Duration `env:"CLEANUP_INTERVAL,default=10m"`

	// Hot reload settings
	EnableHotReload     bool          `env:"ENABLE_HOT_RELOAD,default=false"`
	ConfigPath          string        `env:"CONFIG_PATH,overwrite"`
	ReloadCheckInterval time.Duration `env:"RELOAD_CHECK_INTERVAL,default=10s"`
}

// SecurityConfig defines security settings
type SecurityConfig struct {
	MasterKey          string `env:"MASTER_KEY" secret:"true"`
	TLSCertPath        string `env:"TLS_CERT_PATH"`
	TLSKeyPath         string `env:"TLS_KEY_PATH"`
	EnableTLS          bool   `env:"ENABLE_TLS,default=false"`
	AllowedOrigins     string `env:"ALLOWED_ORIGINS"`
	EnableCORS         bool   `env:"ENABLE_CORS,default=false"`
	JWTSecret          string `env:"JWT_SECRET" secret:"true"`
	APIKeyHeader       string `env:"API_KEY_HEADER,default=Authorization"`
	EnableRateLimiting bool   `env:"ENABLE_RATE_LIMITING,default=true"`
}

// LoggingConfig defines logging settings
type LoggingConfig struct {
	Level      string `env:"LEVEL,default=info"`
	Format     string `env:"FORMAT,default=json"`
	Output     string `env:"OUTPUT,default=stdout"`
	FilePath   string `env:"FILE_PATH"`
	MaxSize    int    `env:"MAX_SIZE,default=100"`
	MaxBackups int    `env:"MAX_BACKUPS,default=3"`
	MaxAge     int    `env:"MAX_AGE,default=7"`
	Compress   bool   `env:"COMPRESS,default=true"`
}

// CacheConfig defines cache settings
type CacheConfig struct {
	Enabled bool `env:"ENABLED,default=true"`
}

// ChatUIConfig defines settings for the embedded chat UI
type ChatUIConfig struct {
	Enabled bool   `env:"ENABLED,default=false"`
	Title   string `env:"TITLE,default=Starport Chat"`
	Theme   string `env:"THEME,default=light"`
}

// Validate performs validation on the configuration
func (c *Config) Validate() error {
	// Validate server config
	if err := c.Server.Validate(); err != nil {
		return err
	}

	// Validate storage config
	if err := c.Storage.Validate(); err != nil {
		return err
	}
	if err := c.Catalog.Validate(); err != nil {
		return err
	}
	if err := c.CredentialSources.Validate(); err != nil {
		return err
	}
	if err := c.Providers.Validate(); err != nil {
		return err
	}

	// Validate rate limiting config
	if err := c.RateLimiting.Validate(); err != nil {
		return err
	}

	// Validate security config
	if err := c.Security.Validate(); err != nil {
		return err
	}

	// Validate logging config
	if err := c.Logging.Validate(); err != nil {
		return err
	}

	// Validate ChatUI config
	if err := c.ChatUI.Validate(); err != nil {
		return err
	}

	return nil
}
