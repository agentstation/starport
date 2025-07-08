package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog/log"
)

// Server represents the HTTP server
type Server struct {
	router     *chi.Mux
	config     *Config
	httpServer *http.Server
}

// New creates a new Server instance
func New(config *Config) *Server {
	s := &Server{
		router: chi.NewRouter(),
		config: config,
	}

	s.setupMiddleware()
	s.setupRoutes()

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.Host, config.Port),
		Handler:      s.router,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}

	return s
}

// setupMiddleware configures all middleware
func (s *Server) setupMiddleware() {
	// Request ID middleware - must be first
	s.router.Use(middleware.RequestID)
	
	// Real IP middleware to get the true client IP
	s.router.Use(middleware.RealIP)
	
	// Logging middleware
	s.router.Use(LoggingMiddleware)
	
	// Recoverer middleware to recover from panics
	s.router.Use(middleware.Recoverer)
	
	// Timeout middleware
	s.router.Use(middleware.Timeout(60 * time.Second))
	
	// CORS middleware
	s.router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.config.CORS.AllowedOrigins,
		AllowedMethods:   s.config.CORS.AllowedMethods,
		AllowedHeaders:   s.config.CORS.AllowedHeaders,
		ExposedHeaders:   s.config.CORS.ExposedHeaders,
		AllowCredentials: s.config.CORS.AllowCredentials,
		MaxAge:           s.config.CORS.MaxAge,
	}))
	
	// Compression middleware
	s.router.Use(middleware.Compress(5))
}

// setupRoutes configures all routes
func (s *Server) setupRoutes() {
	// Health check routes
	s.router.Get("/health/live", s.handleLive)
	s.router.Get("/health/ready", s.handleReady)

	// API routes will be added here
	s.router.Route("/api/v1", func(r chi.Router) {
		// Placeholder for API routes
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"message":"Starport API v1"}`)); err != nil {
				log.Error().Err(err).Msg("failed to write response")
			}
		})
	})
}

// Start starts the HTTP server
func (s *Server) Start() error {
	log.Info().
		Int("port", s.config.Port).
		Msg("starting HTTP server")

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	
	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	log.Info().Msg("shutting down HTTP server")
	
	shutdownCtx, cancel := context.WithTimeout(ctx, s.config.ShutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

// Router returns the chi router for testing
func (s *Server) Router() http.Handler {
	return s.router
}