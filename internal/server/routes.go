package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/agentstation/starport/internal/localauth"
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
		r.Use(s.enforceBudgets)

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

		// Served without credentials, and grouped separately to stay outside
		// the API-key middleware below: bundled brand marks, so plain <img>
		// tags render identity, and the authentication mode, so a client with
		// no key can learn whether it needs one.
		r.Group(func(r chi.Router) {
			r.Get("/logos/{kind}/{id}.svg", s.controllers.Logos.Get)
			r.Get("/auth/mode", s.controllers.Auth.Mode)
		})

		// Every other route requires an API key.
		r.Group(func(r chi.Router) {
			// Apply authentication middleware
			r.Use(s.requireAPIKey)
			r.Use(s.rateLimit)
			r.Use(s.enforceBudgets)

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

			// Catalog authors
			r.With(s.requireAnyScope("models:read")).Get("/authors", s.controllers.Authors.List)
			r.With(s.requireAnyScope("models:read")).Get("/authors/{author}", s.controllers.Authors.Get)

			// Catalog freshness and changes
			r.With(s.requireAnyScope("models:read")).Get("/catalog", s.controllers.Catalog.Metadata)
			r.With(s.requireAnyScope("models:read")).Get("/catalog/changes", s.controllers.Catalog.Changes)

			// Gateway credentials: the operator applies one provider
			// credential for the whole deployment. This needs the admin
			// scope and no gateway API key of the operator's own, because
			// a deployment credential is not a property of any key.
			r.Route("/providers/{provider}/credentials", func(r chi.Router) {
				r.Use(s.requireAdmin)

				r.Get("/", s.controllers.ProviderCredentials.GatewayGet)
				r.Put("/", s.controllers.ProviderCredentials.GatewayPut)
				r.Delete("/", s.controllers.ProviderCredentials.GatewayDelete)
				r.Post("/validate", s.controllers.ProviderCredentials.GatewayValidate)
			})

			// BYOK: a credential one tenant brings for itself. The path says
			// byok because only a tenant-brought credential lives here.
			//
			// The admin scope appears in each scope list because an operator
			// supporting a tenant reaches the tenant's plane by holding admin,
			// not by holding the tenant's own provider scopes. Only "*" is a
			// wildcard in a key's scope set, so admin has to be named here.
			r.Route("/tenants/{tenant_id}/byok", func(r chi.Router) {
				r.Use(s.requireTenantAccess)

				r.With(s.requireAnyScope("provider_keys:read", "admin")).Get("/", s.controllers.ProviderCredentials.BYOKList)
				r.With(s.requireAnyScope("provider_keys:read", "admin")).Get("/{provider}", s.controllers.ProviderCredentials.BYOKGet)
				r.With(s.requireAnyScope("provider_keys:write", "admin")).Put("/{provider}", s.controllers.ProviderCredentials.BYOKPut)
				r.With(s.requireAnyScope("provider_keys:write", "admin")).Delete("/{provider}", s.controllers.ProviderCredentials.BYOKDelete)
				r.With(s.requireAnyScope("provider_keys:write", "admin")).Post("/{provider}/validate", s.controllers.ProviderCredentials.BYOKValidate)
			})

			// Per-provider usage for one account. Spend is an account
			// question: an account holds many keys and no key's total
			// answers for it. It is not a credential route.
			r.Route("/tenants/{tenant_id}/usage", func(r chi.Router) {
				r.Use(s.requireTenantAccess)

				r.With(s.requireAnyScope("activity:read", "admin")).Get("/providers", s.controllers.Activity.ByProvider)
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
				// The authentication switch. It is the only write that
				// can open the gateway, so the controller adds two
				// guards the admin scope does not supply: the request
				// has to come from this machine, and the resulting
				// gateway has to be one an unauthenticated caller
				// cannot reach from the network. It carries its own
				// guard rather than the group's because an already
				// open gateway issues no key that could hold admin,
				// and a switch nobody can reach is a switch stuck on.
				r.With(s.requireSwitchAccess).Put("/auth/mode", s.controllers.Auth.SetMode)

				r.Group(func(r chi.Router) {
					r.Use(s.requireAdmin)

					// API key management
					r.Route("/keys", func(r chi.Router) {
						r.Get("/", s.controllers.Admin.ListKeys)
						r.Post("/", s.controllers.Admin.CreateKey)
						r.Get("/{key_id}", s.controllers.Admin.GetKey)
						r.Put("/{key_id}", s.controllers.Admin.UpdateKey)
						r.Delete("/{key_id}", s.controllers.Admin.DeleteKey)
					})

					// Account management. An account owns limits, the
					// credential strategy, and the keys issued under it, so
					// it is a separate plane from key management above.
					r.Route("/tenants", func(r chi.Router) {
						r.Get("/", s.controllers.Tenants.List)
						r.Post("/", s.controllers.Tenants.Create)
						r.Get("/{tenant_id}", s.controllers.Tenants.Get)
						r.Put("/{tenant_id}", s.controllers.Tenants.Update)
						r.Delete("/{tenant_id}", s.controllers.Tenants.Delete)
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
		})
	})

	// Console session grants. Both routes sit outside every authentication
	// group on purpose: the request itself carries the credential — a ticket in
	// the query string, a token in the body — and a caller presenting either
	// has by definition no session yet.
	mux.Get(localauth.LaunchPath, s.controllers.Launch.Launch)

	// The pasted-token grant. Its path is written out rather than routed
	// through a constant because this is its only use in Go; the console
	// reaches it from TypeScript.
	mux.Post("/console/session", s.controllers.ConsoleSession.Create)

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
// Console Session Grants:
//   GET /launch?lt=<ticket>     - Spend a one-time launch ticket for a session
//   POST /console/session       - Exchange a pasted local admin token for one
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
//   GET  /api/v1/authors                    - List catalog authors
//   GET  /api/v1/authors/{author}           - Get catalog author details
//   GET  /api/v1/logos/{kind}/{id}.svg      - Catalog identity mark (public, cached)
//   GET  /api/v1/auth/mode                  - Whether a gateway API key is required (public)
//   GET  /api/v1/activity                   - List request activity for the authenticated key
//
// Preset Management:
//   GET    /api/v1/presets          - List presets
//   POST   /api/v1/presets          - Create preset (presets:write)
//   GET    /api/v1/presets/{name}   - Get preset
//   PUT    /api/v1/presets/{name}   - Update preset, revision-checked (presets:write)
//   DELETE /api/v1/presets/{name}   - Delete preset (presets:write)
//
// Provider Credentials, operator plane (admin):
//   GET    /api/v1/providers/{provider}/credentials          - Read the deployment credential
//   PUT    /api/v1/providers/{provider}/credentials          - Apply or rotate it
//   DELETE /api/v1/providers/{provider}/credentials          - Remove it
//   POST   /api/v1/providers/{provider}/credentials/validate - Check it against the catalog schema
//
// Provider Credentials, tenant plane (BYOK):
//   GET    /api/v1/tenants/{tenant_id}/byok                       - List the tenant's own credentials
//   GET    /api/v1/tenants/{tenant_id}/byok/{provider}            - Read one
//   PUT    /api/v1/tenants/{tenant_id}/byok/{provider}            - Apply or rotate one
//   DELETE /api/v1/tenants/{tenant_id}/byok/{provider}            - Remove one
//   POST   /api/v1/tenants/{tenant_id}/byok/{provider}/validate   - Check one against the catalog schema
//
// Account Usage:
//   GET    /api/v1/tenants/{tenant_id}/usage/providers - One account's spend grouped by serving provider
//
// Admin API:
//   GET    /api/v1/admin/keys              - List all API keys
//   POST   /api/v1/admin/keys              - Create API key
//   GET    /api/v1/admin/keys/{key_id}     - Get API key details
//   PUT    /api/v1/admin/keys/{key_id}     - Update API key
//   DELETE /api/v1/admin/keys/{key_id}     - Delete API key
//   GET    /api/v1/admin/tenants           - List accounts
//   POST   /api/v1/admin/tenants           - Create an account
//   GET    /api/v1/admin/tenants/{tenant_id}    - Get account details
//   PUT    /api/v1/admin/tenants/{tenant_id}    - Update an account, revision-checked
//   DELETE /api/v1/admin/tenants/{tenant_id}    - Delete an account that holds no keys
//   GET    /api/v1/admin/info              - System information
//   GET    /api/v1/admin/metrics           - System metrics
//   GET    /api/v1/admin/activity          - List request activity across keys
//   GET    /api/v1/admin/providers         - Provider runtime status
//   POST   /api/v1/admin/providers/refresh - Reconcile provider credentials
//   PUT    /api/v1/admin/auth/mode        - Require or stop requiring a gateway API key
