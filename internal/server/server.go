package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/cache"
	"github.com/agentstation/starport/internal/chatui"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/server/handlers"
	"github.com/agentstation/starport/internal/storage"
)

// Server represents the HTTP server with new handler organization
type Server struct {
	router     *chi.Mux
	cfg        *Config
	httpServer *http.Server

	// Core dependencies
	registry   *registry.Registry
	service    proxy.Service
	store      storage.KVStore
	keyManager providers.KeyManager

	// Handler collection
	handlers *handlers.Collection

	// Middleware
	auth *AuthMiddleware

	// ChatUI configuration
	chatUIConfig *chatui.Config
}

// Option configures server options
type Option func(*Server)

// WithCache enables caching with the cache Manager
func WithCache(cm *cache.Manager) Option {
	return func(s *Server) {
		// Wrap service with caching layer using cache manager
		cacheConfig := proxy.CacheConfig{
			EnableChatCache:      true,
			EnableEmbeddingCache: true,
			EnableModelCache:     true,
			EnableProviderCache:  true,
			CacheControlHeader:   "X-Cache-Control",
		}
		s.service = proxy.NewCachedService(s.service, cm, cacheConfig)
		log.Info().Msg("cache manager option applied - service wrapped with caching layer")
	}
}

// WithChatUI enables the ChatUI with the given configuration
func WithChatUI(config *chatui.Config) Option {
	return func(s *Server) {
		s.chatUIConfig = config
	}
}

// New creates a new server instance with improved handler organization
func New(config *Config, reg *registry.Registry, opts ...Option) *Server {
	// Create dependencies
	store := storage.NewMockStore()                      // TODO: Get from config
	keyManager, _ := providers.NewKeyManager(store, nil) // TODO: Get encryption service

	// Create routing with adapter
	router := routing.NewRouter(newRegistryAdapter(reg))

	// Create proxy service
	service := proxy.NewService(reg, router)

	// Create server instance
	s := &Server{
		router:     chi.NewRouter(),
		cfg:        config,
		registry:   reg,
		service:    service,
		store:      store,
		keyManager: keyManager,
		auth:       NewAuthMiddleware(store),
	}

	// Apply options first to get ChatUI config
	for _, opt := range opts {
		opt(s)
	}

	// Create handler collection with ChatUI config
	handlerConfig := handlers.Config{
		Service:       service,
		KeyManager:    keyManager,
		Store:         store,
		ServiceName:   "starport",
		Version:       "1.0.0",
		ChatUIEnabled: s.chatUIConfig != nil,
		Logger:        &log.Logger,
	}
	
	if s.chatUIConfig != nil {
		handlerConfig.ChatUIConfig = *s.chatUIConfig
	}
	
	s.handlers = handlers.NewCollection(handlerConfig)

	// Setup routes using the new centralized routes.go
	s.setupRoutes(s.router)

	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.Host, config.Port),
		Handler:      s.router,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}

	return s
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

func (s *Server) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "The requested endpoint does not exist")
}

func (s *Server) handleMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	dto.WriteError(w, http.StatusMethodNotAllowed, dto.ErrorTypeInvalidRequest, "Method not allowed")
}

// Start starts the HTTP server
func (s *Server) Start() error {
	log.Info().
		Int("port", s.cfg.Port).
		Str("host", s.cfg.Host).
		Msg("starting HTTP server")

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

	// Close registry and cleanup resources
	if err := s.registry.Close(); err != nil {
		log.Error().Err(err).Msg("failed to close registry")
	}

	return nil
}

// Router returns the chi router (useful for testing)
func (s *Server) Router() http.Handler {
	return s.router
}
