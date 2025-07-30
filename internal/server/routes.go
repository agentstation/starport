package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerRoutes registers all routes for the server
// This provides a clear, centralized view of all API endpoints
func (s *Server) registerRoutes(mux *chi.Mux) {

	// Global middleware
	mux.Use(s.setupMiddleware()...)

	// Health check endpoints (no auth required)
	mux.Route("/health", func(r chi.Router) {
		r.Get("/live", s.controllers.Health.Live)
		r.Get("/ready", s.controllers.Health.Ready)
	})

	// OpenAI-compatible API (v1)
	mux.Route("/v1", func(r chi.Router) {

		// Apply authentication middleware for API routes
		r.Use(s.requireAPIKey)

		// Chat completions
		r.Post("/chat/completions", s.controllers.Chat.Create)

		// Embeddings
		r.Post("/embeddings", s.controllers.Embeddings.Create)

		// Models
		r.Get("/models", s.controllers.Models.List)
		r.Get("/models/{model}", s.controllers.Models.Get)
	})

	// OpenRouter-compatible API (api/v1)
	mux.Route("/api/v1", func(r chi.Router) {

		// Apply authentication middleware
		r.Use(s.requireAPIKey)

		// Chat completions with routing
		r.Post("/chat/completions", s.controllers.Chat.Create)

		// Embeddings
		r.Post("/embeddings", s.controllers.Embeddings.Create)

		// Models with enhanced metadata
		r.Get("/models", s.controllers.Models.List)
		r.Get("/models/{model}", s.controllers.Models.Get)
		r.Get("/models/{model}/endpoints", s.controllers.Models.GetEndpoints)

		// Providers metadata
		r.Get("/providers", s.controllers.Providers.List)

		// Key management endpoints
		r.Route("/keys/{key_id}/provider-keys", func(r chi.Router) {
			r.Use(s.requireKeyOwnership) // Additional middleware to verify key ownership

			r.Get("/", s.controllers.ProviderKeys.List)
			r.Post("/", s.controllers.ProviderKeys.Create)
			r.Get("/{provider}", s.controllers.ProviderKeys.Get)
			r.Put("/{provider}", s.controllers.ProviderKeys.Update)
			r.Delete("/{provider}", s.controllers.ProviderKeys.Delete)
			r.Post("/{provider}/validate", s.controllers.ProviderKeys.Validate)
		})

		// Usage endpoints
		r.Route("/keys/{key_id}/usage", func(r chi.Router) {
			r.Use(s.requireKeyOwnership)

			r.Get("/provider-keys", s.controllers.ProviderKeys.GetUsage)
			r.Get("/comparison", s.controllers.ProviderKeys.GetUsageComparison)
		})

		// Admin endpoints (requires admin privileges)
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.requireAdmin)

			// API key management
			r.Route("/keys", func(r chi.Router) {
				r.Get("/", s.controllers.Admin.ListKeys)
				r.Post("/", s.controllers.Admin.CreateKey)
				r.Get("/{key_id}", s.controllers.Admin.GetKey)
				r.Put("/{key_id}", s.controllers.Admin.UpdateKey)
				r.Delete("/{key_id}", s.controllers.Admin.DeleteKey)
			})

			// System information
			r.Get("/info", s.controllers.Admin.SystemInfo)
			r.Get("/metrics", s.controllers.Admin.Metrics)
		})
	})

	// ChatUI endpoints (optional feature)
	if s.controllers.ChatUI != nil {
		mux.Mount("/chat", s.controllers.ChatUI.Routes())
	}

	// Legacy compatibility routes (deprecated)
	mux.Route("/openai/v1", func(r chi.Router) {
		r.Use(s.requireAPIKey)
		r.HandleFunc("/*", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Deprecated", "true")
			w.Header().Set("X-Deprecated-Use", "/v1")

			// Return deprecation notice
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "This endpoint is deprecated. Please use /v1 instead."}`))
		})
	})

	// Catch-all for undefined routes
	mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.handleNotFound(w, r)
	})

	// Method not allowed handler
	mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		s.handleMethodNotAllowed(w, r)
	})
}

// setupMiddleware returns the middleware chain for the server
func (s *Server) setupMiddleware() []func(http.Handler) http.Handler {
	return []func(http.Handler) http.Handler{
		RequestID,                     // Generate request ID
		RealIP,                        // Extract real client IP
		LoggingMiddleware,             // Log requests
		Recoverer,                     // Recover from panics
		SecurityHeaders,               // Add security headers
		SizeLimiter(10 * 1024 * 1024), // 10MB request size limit
		Timeout(s.cfg.RequestTimeout), // Request timeout
		CORS(s.cfg.CORS),              // CORS handling
		Compress(5),                   // Response compression
	}
}

// API Routes Documentation:
//
// Health Endpoints:
//   GET /health/live            - Liveness probe
//   GET /health/ready           - Readiness probe
//
// OpenAI-Compatible API (v1):
//   POST /v1/chat/completions   - Create chat completion
//   POST /v1/embeddings         - Create embeddings
//   GET  /v1/models             - List available models
//   GET  /v1/models/{model}     - Get model details
//
// OpenRouter-Compatible API (api/v1):
//   POST /api/v1/chat/completions           - Create routed chat completion
//   POST /api/v1/embeddings                 - Create embeddings
//   GET  /api/v1/models                     - List models with metadata
//   GET  /api/v1/models/{model}             - Get model details with metadata
//   GET  /api/v1/models/{model}/endpoints   - List provider endpoints for model
//   GET  /api/v1/providers                  - List available providers
//
// Provider Key Management:
//   GET    /api/v1/keys/{key_id}/provider-keys           - List provider keys
//   POST   /api/v1/keys/{key_id}/provider-keys           - Create provider key
//   GET    /api/v1/keys/{key_id}/provider-keys/{provider} - Get provider key
//   PUT    /api/v1/keys/{key_id}/provider-keys/{provider} - Update provider key
//   DELETE /api/v1/keys/{key_id}/provider-keys/{provider} - Delete provider key
//
// Admin API:
//   GET    /api/v1/admin/keys              - List all API keys
//   POST   /api/v1/admin/keys              - Create API key
//   GET    /api/v1/admin/keys/{key_id}     - Get API key details
//   PUT    /api/v1/admin/keys/{key_id}     - Update API key
//   DELETE /api/v1/admin/keys/{key_id}     - Delete API key
//   GET    /api/v1/admin/info              - System information
//   GET    /api/v1/admin/metrics           - System metrics
