// Package server provides HTTP server implementation for Starport.
package server

import (
	"time"
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