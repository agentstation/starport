// Package server provides HTTP server implementation for Starport.
package server

import (
	"time"

	"github.com/agentstation/starport/internal/authmode"
)

// Config holds server configuration
type Config struct {
	// Port to listen on
	Port int `env:"PORT,default=8080"`

	// Host to bind to
	Host string `env:"HOST,default=0.0.0.0"`

	// Read and write timeouts
	ReadTimeout  time.Duration `env:"READ_TIMEOUT,default=10s"`
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT,default=10s"`
	IdleTimeout  time.Duration `env:"IDLE_TIMEOUT,default=120s"`

	// Request timeout for middleware
	RequestTimeout time.Duration `env:"REQUEST_TIMEOUT,default=60s"`

	// Shutdown timeout
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT,default=30s"`

	// MaxRequestSize is the largest request body the gateway reads, in bytes.
	// Application composition supplies it from the loaded configuration, so
	// this field carries no environment tag: a tag here would state a default
	// that never reads the environment and would drift from the real one.
	MaxRequestSize int64

	// MaxFileUploadSize is the largest file upload the gateway accepts, in
	// bytes. It carries no environment tag for the same reason MaxRequestSize
	// does not: application composition supplies it from the loaded file
	// configuration, and a tag here would state a second default that never
	// reads the environment.
	MaxFileUploadSize int64

	// Maximum aggregate size of HTTP request headers.
	MaxHeaderBytes int `env:"MAX_HEADER_BYTES,default=1048576"`

	// Rate limiting configuration. Enforcement happens after API key
	// authentication and uses the authenticated API key ID, not the raw secret.
	EnableRateLimiting         bool          `env:"ENABLE_RATE_LIMITING,default=false"`
	RateLimitRequestsPerWindow int64         `env:"RATE_LIMIT_REQUESTS_PER_WINDOW,default=0"`
	RateLimitWindow            time.Duration `env:"RATE_LIMIT_WINDOW,default=1m"`

	// AuthMode selects whether a request must carry a gateway API key. It is
	// the mode the gateway starts under; the console can change the running
	// mode afterwards, and the middleware reads the live policy rather than
	// this field.
	AuthMode authmode.Mode

	// AuthModeSource names where AuthMode came from, so the startup banner and
	// the console can say which thing an operator has to change.
	AuthModeSource authmode.Source

	// AuthModeStore persists a mode the console sets, so the change outlives
	// the process that accepted it. A nil store means the mode cannot be
	// changed at runtime, and the switch reports that rather than accepting a
	// change it would forget.
	AuthModeStore authmode.Repository

	// UnauthenticatedScopes lists the scopes a request holds while the running
	// mode is disabled. An empty list means identity.DefaultAnonymousScopes.
	UnauthenticatedScopes []string

	// AllowRemoteNoAuth is the operator's acknowledgment that an
	// unauthenticated gateway may bind an address the network can reach. It is
	// the same acknowledgment startup validation reads, and the runtime switch
	// reads it for the same reason.
	AllowRemoteNoAuth bool

	// CORS configuration
	CORS CORSConfig
}

// CORSConfig holds CORS configuration
type CORSConfig struct {
	// AllowedOrigins is a list of origins a cross-domain request can be executed from
	AllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS,default=*"`

	// AllowedMethods is a list of methods the client is allowed to use with cross-domain requests
	AllowedMethods []string `env:"CORS_ALLOWED_METHODS,default=GET,POST,PUT,DELETE,OPTIONS"`

	// AllowedHeaders is list of non simple headers the client is allowed to use with cross-domain requests
	AllowedHeaders []string `env:"CORS_ALLOWED_HEADERS,default=Accept,Authorization,Content-Type,X-CSRF-Token"`

	// ExposedHeaders indicates which headers are safe to expose to the API of a CORS API specification
	ExposedHeaders []string `env:"CORS_EXPOSED_HEADERS,default="`

	// AllowCredentials indicates whether the request can include user credentials
	AllowCredentials bool `env:"CORS_ALLOW_CREDENTIALS,default=true"`

	// MaxAge indicates how long (in seconds) the results of a preflight request can be cached
	MaxAge int `env:"CORS_MAX_AGE,default=300"`
}
