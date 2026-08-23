package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/console"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/presets"
	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/tenant"
	"github.com/agentstation/starport/internal/usage"
)

var (
	// ErrConfigRequired reports an absent HTTP server configuration.
	ErrConfigRequired = errors.New("server config is required")
	// ErrServiceRequired reports an absent gateway use-case service.
	ErrServiceRequired = errors.New("gateway service is required")
	// ErrIdentitiesRequired reports an absent identity repository.
	ErrIdentitiesRequired = errors.New("identity repository is required")
	// ErrTenantsRequired reports an absent tenant repository.
	ErrTenantsRequired = errors.New("tenant repository is required")
	// ErrProviderKeysRequired reports an absent provider-key service.
	ErrProviderKeysRequired = errors.New("provider key service is required")
	// ErrRateLimitsRequired reports an absent rate-limit repository.
	ErrRateLimitsRequired = errors.New("rate-limit repository is required")
	// ErrProviderOperationsRequired reports an absent provider operations port.
	ErrProviderOperationsRequired = errors.New("provider operations are required")
)

// Server represents the HTTP server with new handler organization
type Server struct {
	router     *chi.Mux
	cfg        *Config
	httpServer *http.Server

	// Ready application dependencies
	service            proxy.Proxy
	identities         identity.Repository
	tenants            tenant.Repository
	providerKeys       byok.ProviderKeys
	rateLimits         ratelimit.Repository
	usage              usage.Repository
	providerOperations controllers.ProviderOperations

	// Handler collection
	controllers *controllers.Controllers

	// Middleware
	auth *AuthMiddleware
}

// Dependencies contains ready application ports for the HTTP adapter.
type Dependencies struct {
	Service            proxy.Proxy
	Identities         identity.Repository
	Tenants            tenant.Repository
	ProviderKeys       byok.ProviderKeys
	RateLimits         ratelimit.Repository
	ProviderOperations controllers.ProviderOperations
	Console            console.PageServer
	// Usage serves recorded request activity. A nil repository degrades
	// the activity and metrics endpoints to 503, loudly.
	Usage usage.Repository
	// Catalog serves snapshot freshness, diffs, and forced acquisition. A
	// nil port degrades the catalog endpoints to 503, loudly.
	Catalog controllers.CatalogOperations
	// Presets serves stored preset management. A nil repository degrades
	// the preset endpoints to 503, loudly.
	Presets presets.Repository
}

// New creates an HTTP adapter from ready application dependencies.
func New(config *Config, dependencies Dependencies) (*Server, error) {
	if config == nil {
		return nil, ErrConfigRequired
	}
	if dependencies.Service == nil {
		return nil, ErrServiceRequired
	}
	if dependencies.Identities == nil {
		return nil, ErrIdentitiesRequired
	}
	if dependencies.Tenants == nil {
		return nil, ErrTenantsRequired
	}
	if dependencies.ProviderKeys == nil {
		return nil, ErrProviderKeysRequired
	}
	if dependencies.RateLimits == nil {
		return nil, ErrRateLimitsRequired
	}
	if dependencies.ProviderOperations == nil {
		return nil, ErrProviderOperationsRequired
	}

	s := &Server{
		router:             chi.NewRouter(),
		cfg:                config,
		service:            dependencies.Service,
		identities:         dependencies.Identities,
		tenants:            dependencies.Tenants,
		providerKeys:       dependencies.ProviderKeys,
		rateLimits:         dependencies.RateLimits,
		usage:              dependencies.Usage,
		providerOperations: dependencies.ProviderOperations,
	}
	s.auth = NewAuthMiddleware(s.identities)

	handlerConfig := controllers.Config{
		Service:            s.service,
		ProviderKeys:       s.providerKeys,
		Identities:         s.identities,
		Tenants:            s.tenants,
		Usage:              dependencies.Usage,
		ProviderOperations: s.providerOperations,
		Catalog:            dependencies.Catalog,
		Presets:            dependencies.Presets,
		ServiceName:        "starport",
		Version:            "1.0.0",
		Console:            dependencies.Console,
	}
	s.controllers = controllers.NewControllers(handlerConfig)

	// Register routes
	s.registerRoutes(s.router)

	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:           fmt.Sprintf("%s:%d", config.Host, config.Port),
		Handler:        s.router,
		ReadTimeout:    config.ReadTimeout,
		WriteTimeout:   config.WriteTimeout,
		IdleTimeout:    config.IdleTimeout,
		MaxHeaderBytes: config.MaxHeaderBytes,
	}

	return s, nil
}

// Middleware methods that routes.go expects

func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return s.auth.RequireAPIKey(next)
}

func (s *Server) requireKeyOwnership(next http.Handler) http.Handler {
	return s.auth.RequireKeyOwnership(next)
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.auth.RequireAdmin(next)
}

func (s *Server) requireAnyScope(scopes ...string) func(http.Handler) http.Handler {
	return s.auth.RequireAnyScope(scopes...)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeProtocolError(w, r, http.StatusNotFound, "not_found_error", "The requested endpoint does not exist")
}

func (s *Server) handleMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeProtocolError(w, r, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed")
}

// Start starts the HTTP server
func (s *Server) Start() error {
	log.Info().
		Int("port", s.cfg.Port).
		Str("host", s.cfg.Host).
		Msg("starting HTTP server")

	if s.controllers.Console != nil {
		host := s.cfg.Host
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "localhost"
		}
		log.Info().
			Str("url", fmt.Sprintf("http://%s:%d", host, s.cfg.Port)).
			Msg("console ready")
	}

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	log.Info().Msg("shutting down HTTP server")

	shutdownCtx, cancel := context.WithTimeout(ctx, s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

// Router returns the chi router (useful for testing)
func (s *Server) Router() http.Handler {
	return s.router
}
