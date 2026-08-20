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
		r.Use(selectProtocol(openAIProtocol))

		// Apply authentication middleware for API routes
		r.Use(s.requireAPIKey)
		r.Use(s.rateLimit)

		// Chat completions
		r.With(s.requireAnyScope("chat:write")).Post("/chat/completions", s.controllers.Chat.Create)

		// Embeddings
		r.With(s.requireAnyScope("chat:write", "embeddings:write")).Post("/embeddings", s.controllers.Embeddings.Create)

		// Models
		r.With(s.requireAnyScope("models:read")).Get("/models", s.controllers.Models.List)
		r.With(s.requireAnyScope("models:read")).Get("/models/{model}", s.controllers.Models.Get)
	})

	// OpenRouter-compatible API (api/v1)
	mux.Route("/api/v1", func(r chi.Router) {
		r.Use(selectProtocol(openRouterProtocol))

		// Apply authentication middleware
		r.Use(s.requireAPIKey)
		r.Use(s.rateLimit)

		// Chat completions with routing
		r.With(s.requireAnyScope("chat:write")).Post("/chat/completions", s.controllers.OpenRouterChat.Create)

		// Embeddings
		r.With(s.requireAnyScope("chat:write", "embeddings:write")).Post("/embeddings", s.controllers.OpenRouterEmbeddings.Create)

		// Models with enhanced metadata
		r.With(s.requireAnyScope("models:read")).Get("/models", s.controllers.OpenRouterModels.List)
		r.With(s.requireAnyScope("models:read")).Get("/models/{model}", s.controllers.OpenRouterModels.Get)
		r.With(s.requireAnyScope("models:read")).Get("/models/{model}/endpoints", s.controllers.OpenRouterModels.GetEndpoints)

		// Providers metadata
		r.With(s.requireAnyScope("models:read")).Get("/providers", s.controllers.Providers.List)

		// Catalog freshness and changes
		r.With(s.requireAnyScope("models:read")).Get("/catalog", s.controllers.Catalog.Metadata)
		r.With(s.requireAnyScope("models:read")).Get("/catalog/changes", s.controllers.Catalog.Changes)

		// Key management endpoints
		r.Route("/keys/{key_id}/provider-keys", func(r chi.Router) {
			r.Use(s.requireKeyOwnership) // Additional middleware to verify key ownership

			r.With(s.requireAnyScope("provider_keys:read", "keys:read")).Get("/", s.controllers.ProviderKeys.List)
			r.With(s.requireAnyScope("provider_keys:write", "keys:write")).Post("/", s.controllers.ProviderKeys.Create)
			r.With(s.requireAnyScope("provider_keys:read", "keys:read")).Get("/{provider}", s.controllers.ProviderKeys.Get)
			r.With(s.requireAnyScope("provider_keys:write", "keys:write")).Put("/{provider}", s.controllers.ProviderKeys.Update)
			r.With(s.requireAnyScope("provider_keys:write", "keys:write")).Delete("/{provider}", s.controllers.ProviderKeys.Delete)
			r.With(s.requireAnyScope("provider_keys:write", "keys:write")).Post("/{provider}/validate", s.controllers.ProviderKeys.Validate)
		})

		// Usage endpoints
		r.Route("/keys/{key_id}/usage", func(r chi.Router) {
			r.Use(s.requireKeyOwnership)

			r.With(s.requireAnyScope("provider_keys:read", "keys:read")).Get("/provider-keys", s.controllers.ProviderKeys.GetUsage)
			r.With(s.requireAnyScope("provider_keys:read", "keys:read")).Get("/comparison", s.controllers.ProviderKeys.GetUsageComparison)
		})

		// Preset management: any authenticated key reads, writes need the
		// presets:write scope (the admin wildcard scope satisfies it).
		r.Route("/presets", func(r chi.Router) {
			r.Get("/", s.controllers.Presets.List)
			r.Get("/{name}", s.controllers.Presets.Get)
			r.With(s.requireAnyScope("presets:write")).Post("/", s.controllers.Presets.Create)
			r.With(s.requireAnyScope("presets:write")).Put("/{name}", s.controllers.Presets.Update)
			r.With(s.requireAnyScope("presets:write")).Delete("/{name}", s.controllers.Presets.Delete)
		})

		// Request activity for the authenticated key
		r.With(s.requireAnyScope("activity:read")).Get("/activity", s.controllers.Activity.List)

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
			r.Get("/activity", s.controllers.Activity.AdminList)
			r.Get("/providers", s.controllers.ProviderOperations.Status)
			r.Post("/providers/refresh", s.controllers.ProviderOperations.Refresh)
			r.Post("/catalog/refresh", s.controllers.Catalog.Refresh)
		})
	})

	// Console pages and assets (optional feature)
	if s.controllers.Console != nil {
		s.controllers.Console.Register(mux)
	}

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
		RequestID,         // Generate request ID
		ClientIP,          // Trust only the direct TCP peer address
		LoggingMiddleware, // Log requests
		Recoverer,         // Recover from panics
		SecurityHeaders,   // Add security headers
		SizeLimiter(s.cfg.MaxRequestSize),
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
//   GET  /api/v1/activity                   - List request activity for the authenticated key
//
// Preset Management:
//   GET    /api/v1/presets          - List presets
//   POST   /api/v1/presets          - Create preset (presets:write)
//   GET    /api/v1/presets/{name}   - Get preset
//   PUT    /api/v1/presets/{name}   - Update preset, revision-checked (presets:write)
//   DELETE /api/v1/presets/{name}   - Delete preset (presets:write)
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
//   GET    /api/v1/admin/activity          - List request activity across keys
//   GET    /api/v1/admin/providers         - Provider runtime status
//   POST   /api/v1/admin/providers/refresh - Reconcile provider credentials
