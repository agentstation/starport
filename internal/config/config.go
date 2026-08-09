// Package config provides configuration management for Starport.
// It supports loading configuration from environment variables and .env files,
// with comprehensive validation and hot reload capabilities for rate limiting rules.
package config

import (
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Config represents the complete application configuration
type Config struct {
	Server       ServerConfig       `env:",prefix=SERVER_"`
	Storage      StorageConfig      `env:",prefix=STORAGE_"`
	Catalog      CatalogConfig      `env:",prefix=CATALOG_"`
	Providers    ProvidersConfig    `env:",prefix=PROVIDERS_"`
	RateLimiting RateLimitingConfig `env:",prefix=RATE_LIMITING_"`
	Security     SecurityConfig     `env:",prefix=SECURITY_"`
	Logging      LoggingConfig      `env:",prefix=LOGGING_"`
	Cache        CacheConfig        `env:",prefix=CACHE_"`
	ChatUI       ChatUIConfig       `env:",prefix=CHATUI_"`
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
	URL            string        `env:"URL,default=valkey://localhost:6379"`
	MaxConnections int           `env:"MAX_CONNECTIONS,default=50"`
	MinIdleConns   int           `env:"MIN_IDLE_CONNS,default=10"`
	DialTimeout    time.Duration `env:"DIAL_TIMEOUT,default=5s"`
	ReadTimeout    time.Duration `env:"READ_TIMEOUT,default=3s"`
	WriteTimeout   time.Duration `env:"WRITE_TIMEOUT,default=3s"`
	IdleTimeout    time.Duration `env:"IDLE_TIMEOUT,default=5m"`
	ClusterMode    bool          `env:"CLUSTER_MODE,default=false"`
	Password       string        `env:"PASSWORD"`
}

// ProvidersConfig defines LLM provider settings
type ProvidersConfig struct {
	OpenAI         ProviderConfig `env:",prefix=OPENAI_"`
	Anthropic      ProviderConfig `env:",prefix=ANTHROPIC_"`
	GoogleAIStudio ProviderConfig `env:",prefix=GOOGLE_AI_STUDIO_"`
	GoogleVertexAI ProviderConfig `env:",prefix=GOOGLE_VERTEX_"`
	Groq           ProviderConfig `env:",prefix=GROQ_"`
	Mistral        ProviderConfig `env:",prefix=MISTRAL_"`
	Azure          ProviderConfig `env:",prefix=AZURE_OPENAI_"`
	Ollama         ProviderConfig `env:",prefix=OLLAMA_"`
}

// ProviderConfig defines settings for a single LLM provider
type ProviderConfig struct {
	BaseURL        string        `env:"BASE_URL"`
	APIKey         string        `env:"API_KEY"`
	Timeout        time.Duration `env:"TIMEOUT,default=30s"`
	MaxConnections int           `env:"MAX_CONNECTIONS,default=100"`
	Enabled        bool          `env:"ENABLED"` // Used for optional providers like Ollama
	ProjectID      string        `env:"PROJECT_ID"`
	Location       string        `env:"LOCATION"`
}

// ProviderEntry binds external operator configuration to one exact Starmap provider ID.
type ProviderEntry struct {
	ProviderID catalogs.ProviderID
	Config     ProviderConfig
}

// Entries returns all supported external configuration slots. Adapter
// semantics and provider membership remain outside the configuration package.
func (c ProvidersConfig) Entries() []ProviderEntry {
	return []ProviderEntry{
		{ProviderID: catalogs.ProviderIDOpenAI, Config: c.OpenAI},
		{ProviderID: catalogs.ProviderIDAnthropic, Config: c.Anthropic},
		{ProviderID: catalogs.ProviderIDGoogleAIStudio, Config: c.GoogleAIStudio},
		{ProviderID: catalogs.ProviderIDGoogleVertex, Config: c.GoogleVertexAI},
		{ProviderID: catalogs.ProviderIDGroq, Config: c.Groq},
		{ProviderID: catalogs.ProviderIDMistralAI, Config: c.Mistral},
		{ProviderID: catalogs.ProviderIDAzureOpenAI, Config: c.Azure},
		{ProviderID: catalogs.ProviderIDOllama, Config: c.Ollama},
	}
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
	MasterKey          string `env:"MASTER_KEY"`
	BootstrapAPIKey    string `env:"BOOTSTRAP_API_KEY"`
	TLSCertPath        string `env:"TLS_CERT_PATH"`
	TLSKeyPath         string `env:"TLS_KEY_PATH"`
	EnableTLS          bool   `env:"ENABLE_TLS,default=false"`
	AllowedOrigins     string `env:"ALLOWED_ORIGINS"`
	EnableCORS         bool   `env:"ENABLE_CORS,default=false"`
	JWTSecret          string `env:"JWT_SECRET"`
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
