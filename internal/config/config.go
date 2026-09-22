// Package config provides configuration management for Starport.
// It supports loading configuration from environment variables and .env files,
// with comprehensive validation and hot reload capabilities for rate limiting rules.
package config

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/credentials"
)

// Config represents the complete application configuration
type Config struct {
	Server            ServerConfig            `env:",prefix=SERVER_"`
	Storage           StorageConfig           `env:",prefix=STORAGE_"`
	Catalog           CatalogConfig           `env:",prefix=CATALOG_"`
	CredentialSources CredentialSourcesConfig `env:",prefix=CREDENTIAL_SOURCES_"`
	Providers         ProvidersConfig
	RateLimiting      RateLimitingConfig  `env:",prefix=RATE_LIMITING_"`
	Security          SecurityConfig      `env:",prefix=SECURITY_"`
	Logging           LoggingConfig       `env:",prefix=LOGGING_"`
	Cache             CacheConfig         `env:",prefix=CACHE_"`
	Files             FilesConfig         `env:",prefix=FILES_"`
	Jobs              JobsConfig          `env:",prefix=JOBS_"`
	Console           ConsoleConfig       `env:",prefix=CONSOLE_"`
	Identity          IdentityConfig      `env:",prefix=IDENTITY_"`
	Telemetry         TelemetryConfig     `env:",prefix=TELEMETRY_"`
	Audit             AuditConfig         `env:",prefix=AUDIT_"`
	Events            EventsConfig        `env:",prefix=EVENTS_"`
	Guardrails        GuardrailsConfig    `env:",prefix=GUARDRAILS_"`
	SemanticCache     SemanticCacheConfig `env:",prefix=SEMANTIC_CACHE_"`

	providerEnvironment  environmentLookup
	credentialResolver   *credentials.Resolver
	credentialResolverMu *sync.Mutex

	// authModeFromFlag records that a command-line flag, and not the
	// environment, stated the authentication mode. It is unexported so the
	// environment cannot set it, which is the whole point: it separates the
	// two sources that write the same field.
	authModeFromFlag bool
}

// AuthModeSource names where the stated authentication mode came from, or
// SourceUnset when nobody stated one. Startup passes it to authmode.Resolve,
// which decides whether a mode an operator stored from the console applies.
func (c *Config) AuthModeSource() authmode.Source {
	switch {
	case c == nil:
		return authmode.SourceUnset
	case c.authModeFromFlag:
		return authmode.SourceFlag
	case c.Security.AuthMode != "":
		return authmode.SourceConfig
	default:
		return authmode.SourceUnset
	}
}

// Metrics exposure modes for TelemetryConfig.
const (
	// TelemetryMetricsOn serves GET /metrics to every caller. It is the
	// default: the labels carry no caller identity, so the scrape exposes
	// deployment aggregates, not tenant activity.
	TelemetryMetricsOn = "on"
	// TelemetryMetricsAdmin requires the admin scope on GET /metrics.
	TelemetryMetricsAdmin = "admin"
	// TelemetryMetricsOff removes the route.
	TelemetryMetricsOff = "off"
)

// TelemetryConfig selects the observability export surfaces. The metric
// vocabulary itself lives in internal/telemetry; this section only states
// which surfaces a deployment serves.
type TelemetryConfig struct {
	// Metrics states who may read the Prometheus scrape at GET /metrics:
	// "on" (default), "admin", or "off".
	Metrics string `env:"METRICS,default=on"`

	// TracesEndpoint holds the OTLP endpoint the standard OpenTelemetry
	// environment names. The variables are OTEL_EXPORTER_OTLP_ENDPOINT and
	// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, deliberately unprefixed: they are
	// the cross-vendor contract every collector documents. Empty means the
	// tracer stays a no-op. The loader fills this field; it carries no env
	// tag because the gateway prefix does not apply.
	TracesEndpoint string

	// UsageExport names where finalized usage records stream. An http or
	// https URL posts NDJSON batches; any other value is a file path that
	// NDJSON lines append to. Empty means no export.
	UsageExport string `env:"USAGE_EXPORT"`
}

// Validate refuses a metrics mode the router would silently read as "on".
func (c TelemetryConfig) Validate() error {
	switch c.Metrics {
	case "", TelemetryMetricsOn, TelemetryMetricsAdmin, TelemetryMetricsOff:
		return nil
	default:
		return fmt.Errorf("telemetry metrics mode %q is not one of on, admin, off", c.Metrics)
	}
}

// DefaultMaxRequestSize is the largest request body the gateway reads, in
// bytes. A caller attaches media as base64 inside the JSON body, and base64
// grows a payload by a third, so the limit has to hold the grown form. The
// largest media file the provider APIs commonly accept is a 25 MB audio file,
// which reaches about 33,333,336 bytes as base64. This value clears that with
// room for the surrounding request.
//
// The tag on MaxRequestSize repeats the number because a struct tag holds a
// literal. TestBodyLimitDefaultMatchesItsConstant proves the two agree.
const DefaultMaxRequestSize int64 = 33554432

// ServerConfig defines HTTP server settings
type ServerConfig struct {
	Port              int           `env:"PORT,default=8080"`
	Host              string        `env:"HOST,default=127.0.0.1"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT,default=30s"`
	WriteTimeout      time.Duration `env:"WRITE_TIMEOUT,default=30s"`
	IdleTimeout       time.Duration `env:"IDLE_TIMEOUT,default=120s"`
	RequestTimeout    time.Duration `env:"REQUEST_TIMEOUT,default=60s"`
	MaxRequestSize    int64         `env:"MAX_REQUEST_SIZE,default=33554432"`
	MaxHeaderBytes    int           `env:"MAX_HEADER_BYTES,default=1048576"`
	ShutdownTimeout   time.Duration `env:"SHUTDOWN_TIMEOUT,default=30s"`
	EnableProfiling   bool          `env:"ENABLE_PROFILING,default=false"`
	EnableHealthCheck bool          `env:"ENABLE_HEALTH_CHECK,default=true"`
}

// StorageConfig defines storage backend settings. Mode selects the
// key-value store; SQL selects its relational twin, which pairs an embedded
// SQLite database with a network connect the way Badger pairs with Valkey.
type StorageConfig struct {
	Mode   string       `env:"MODE,default=badger"`
	Badger BadgerConfig `env:",prefix=BADGER_"`
	Valkey ValkeyConfig `env:",prefix=VALKEY_"`
	SQL    SQLConfig    `env:",prefix=SQL_"`
}

// SQLConfig defines relational store settings.
type SQLConfig struct {
	Mode     string            `env:"MODE,default=sqlite"`
	SQLite   SQLiteConfig      `env:",prefix=SQLITE_"`
	Postgres SQLPostgresConfig `env:",prefix=POSTGRES_"`
	MySQL    SQLMySQLConfig    `env:",prefix=MYSQL_"`
}

// SQLiteConfig defines the embedded relational store's settings. An empty
// path keeps the database in memory, which is the development runtime's
// choice.
type SQLiteConfig struct {
	Path string `env:"PATH,overwrite"`
}

// SQLPostgresConfig defines the PostgreSQL connect's settings.
type SQLPostgresConfig struct {
	URL string `env:"URL" redact:"url"`
}

// SQLMySQLConfig defines the MySQL connect's settings. The DSN embeds the
// password, so inspection redacts it whole.
type SQLMySQLConfig struct {
	DSN string `env:"DSN" secret:"true"`
}

// BadgerConfig defines Badger DB settings
type BadgerConfig struct {
	Path           string        `env:"PATH,overwrite"`
	SyncWrites     bool          `env:"SYNC_WRITES,default=false"`
	Compression    string        `env:"COMPRESSION,default=snappy"`
	GCInterval     time.Duration `env:"GC_INTERVAL,default=5m"`
	GCDiscardRatio float64       `env:"GC_DISCARD_RATIO,default=0.5"`
	inMemory       bool
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
		result[providerID] = provider
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

// EnableProvider marks one exact catalog provider for operator credential
// resolution. Catalog membership and adapter activation remain independent.
func (c *Config) EnableProvider(providerID catalogs.ProviderID) {
	if c.Providers == nil {
		c.Providers = make(ProvidersConfig)
	}
	provider := c.Providers[providerID]
	provider.Enabled = true
	c.Providers[providerID] = provider
}

// RateLimitingConfig defines the global default request window. Per-key
// request limits and budgets live on the identity record and beat these
// defaults.
type RateLimitingConfig struct {
	DefaultRequestsPerMinute int           `env:"DEFAULT_REQUESTS_PER_MINUTE,default=60"`
	WindowSize               time.Duration `env:"WINDOW_SIZE,default=1m"`
}

// AuthMode selects whether a request must carry a gateway API key.
//
// The type is an alias and not a second spelling. A configuration value, a
// command-line flag, and the console switch all state the same decision, and
// internal/authmode owns it so the three cannot drift apart.
type AuthMode = authmode.Mode

const (
	// AuthModeRequired refuses every request that carries no valid gateway
	// API key. It is the default, and the zero value resolves to it.
	AuthModeRequired = authmode.Required
	// AuthModeDisabled serves every request without checking for a key. The
	// key check does not run at all, so a request carrying a key is treated
	// exactly like one that carries none.
	AuthModeDisabled = authmode.Disabled
)

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

	// AuthMode selects whether the gateway requires a gateway API key. It
	// carries no default, so an unset value stays empty and startup can tell
	// "the operator said required" from "the operator said nothing". Only the
	// second yields to a mode an operator stored from the console.
	AuthMode AuthMode `env:"AUTH_MODE"`
	// AllowRemoteNoAuth is the second, explicit acknowledgment that an
	// unauthenticated gateway may bind an address the network can reach.
	// Without it, startup refuses that combination; see the tripwire in
	// validation.go.
	AllowRemoteNoAuth bool `env:"ALLOW_REMOTE_NO_AUTH,default=false"`
	// UnauthenticatedScopes lists the scopes a request holds while AuthMode
	// is disabled. An empty list means the built-in default, which is every
	// account scope and never admin. An operator who wants the admin plane
	// open without a key has to name "admin" here.
	UnauthenticatedScopes []string `env:"UNAUTHENTICATED_SCOPES"`
	// LocalTokenPath is the file holding this machine's local admin token.
	//
	// It carries no environment tag on purpose. The gateway reads it from here
	// and the CLI reads it from Paths, and both derive from one function, so
	// the two cannot disagree about where the credential lives. A second knob
	// for this one file would make disagreeing possible, and a CLI that rotates
	// a token the running gateway never reads is worse than no command at all.
	// An operator who needs the file elsewhere moves everything with
	// STARPORT_CONFIG_DIR.
	LocalTokenPath string `json:"local_token_path"`

	// localTokenReadOnly forbids this process from writing the local admin
	// token file. Only ConfigureDevelopmentRuntime sets it: a development
	// gateway promises to create no files, so it reads the machine's token
	// when one exists — keeping `starport auth token` and the console paste
	// path in agreement with it — and holds an ephemeral in-memory token
	// when the machine has none. It is unexported so no environment variable
	// or configuration file can select it for a serving gateway, whose token
	// the CLI must always find in the file.
	localTokenReadOnly bool
}

// LocalTokenReadOnly reports whether this process must not write the local
// admin token file: read the machine's token if it exists, hold an ephemeral
// one otherwise.
func (c SecurityConfig) LocalTokenReadOnly() bool { return c.localTokenReadOnly }

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

// ConsoleConfig defines settings for the embedded web console
type ConsoleConfig struct {
	Enabled bool `env:"ENABLED,default=true"`
}

// Validate performs validation on the configuration
func (c *Config) Validate() error {
	c.prepareCredentialResolver()
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

	// Validate the file byte store selection
	if err := c.Files.Validate(); err != nil {
		return err
	}

	// Validate the telemetry exposure selection
	if err := c.Telemetry.Validate(); err != nil {
		return err
	}

	// Validate the semantic cache selection
	if err := c.SemanticCache.Validate(); err != nil {
		return err
	}

	// Exposure is the one decision no single section can make: it reads the
	// authentication mode against the bind address.
	return c.validateAuthenticationExposure()
}
