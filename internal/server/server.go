package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/console"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/presets"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/telemetry"
	"github.com/agentstation/starport/internal/usage"
)

var (
	// ErrConfigRequired reports an absent HTTP server configuration.
	ErrConfigRequired = errors.New("server config is required")
	// ErrServiceRequired reports an absent gateway use-case service.
	ErrServiceRequired = errors.New("gateway service is required")
	// ErrAPIKeysRequired reports an absent API key repository.
	ErrAPIKeysRequired = errors.New("API key repository is required")
	// ErrAccountsRequired reports an absent account repository.
	ErrAccountsRequired = errors.New("account repository is required")
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
	apiKeys            apikey.Repository
	accounts           account.Repository
	providerKeys       keyring.ProviderKeys
	rateLimits         ratelimit.Repository
	usage              usage.Repository
	providerOperations controllers.ProviderOperations
	telemetry          *telemetry.Metrics
	tracing            *telemetry.Tracing
	events             controllers.EventEmitter

	// teamBudgets reads the stored spend budget of one team, or is nil when
	// the deployment configured no identity plane: a key may then carry a
	// team attribution, but nothing meters it.
	teamBudgets func(ctx context.Context, teamID string) (*limits.TeamBudget, error)

	// Handler collection
	controllers *controllers.Controllers

	// Middleware
	auth *AuthMiddleware

	// authPolicy is the running authentication mode. It is the server's copy
	// of one shared value: the middleware reads it per request and the console
	// switch writes it, so a change reaches traffic without a restart.
	authPolicy *authmode.Policy
}

// Dependencies contains ready application ports for the HTTP adapter.
type Dependencies struct {
	Service            proxy.Proxy
	APIKeys            apikey.Repository
	Accounts           account.Repository
	ProviderKeys       keyring.ProviderKeys
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
	// Templates serves account templates: the named creation defaults an
	// account can be stamped from. A nil repository degrades the template
	// endpoints to 503, loudly.
	Templates account.TemplateRepository
	// Files serves the stored file surface. A nil service degrades the file
	// endpoints to 503, loudly: the routes stay registered so a caller reads
	// that this deployment configured no file storage rather than that the
	// gateway has no files API.
	Files *files.Service
	// Jobs serves work that outlives its request. A nil service degrades the
	// video endpoints to 503, loudly, for the same reason a nil file service
	// does: the routes stay registered so a caller reads that this deployment
	// configured no job store rather than that the gateway has no video API.
	Jobs *jobs.Service
	// Batches serves the batch surface. A nil service degrades the batch
	// endpoints to 503, loudly, for the same reason a nil job service does.
	Batches *jobs.BatchService

	// FileBackend names the blob backend behind that service, for the admin
	// surface to report. An empty name reads as no file storage at all.
	FileBackend string
	// LocalGate redeems console launch tickets and verifies console sessions
	// against this machine's local admin token. A nil gate refuses every
	// launch and every session cookie, which is the right answer for a
	// gateway assembled without one: the bearer key path is unaffected.
	LocalGate *localauth.Gate
	// IdentityAuth is the OAuth acquisition path, or nil when this
	// deployment configured no identity provider. The identity routes stay
	// mounted either way and refuse with the operator's answer when nil.
	IdentityAuth controllers.IdentityAuthenticator
	// Identity holds the durable people plane: users, teams, memberships,
	// and account grants. Zero repositories — no identity provider configured —
	// degrade the members and teams endpoints to 503, loudly, so the
	// console reads "not configured" rather than "nobody is here".
	Identity identity.Repositories
	// Telemetry serves the Prometheus scrape and counts budget refusals. A
	// nil surface removes the /metrics route and observes nothing, which is
	// what a deployment that turned metrics off gets.
	Telemetry *telemetry.Metrics

	// Tracing starts the request span and hands the tracer to the routing and
	// execution seams. A nil tracer disables tracing without a guard anywhere
	// else.
	Tracing *telemetry.Tracing

	// Audit records admin mutations and serves the trail back. A nil trail
	// records nothing and degrades the audit listing to 503, loudly.
	Audit controllers.AuditTrail

	// Events pushes budget and key lifecycle events to the configured
	// webhook endpoints. A nil emitter pushes nothing, which is the
	// deployment with no endpoint configured.
	Events controllers.EventEmitter

	// Webhooks reports the delivery state of that surface for the admin
	// summary. A nil reporter reads as webhooks off.
	Webhooks controllers.WebhookReporter

	// Deployment is what the admin surface states about the configured
	// storage, telemetry, guardrail, and retention settings.
	Deployment controllers.Deployment
}

// New creates an HTTP adapter from ready application dependencies.
func New(config *Config, dependencies Dependencies) (*Server, error) {
	if config == nil {
		return nil, ErrConfigRequired
	}
	if dependencies.Service == nil {
		return nil, ErrServiceRequired
	}
	if dependencies.APIKeys == nil {
		return nil, ErrAPIKeysRequired
	}
	if dependencies.Accounts == nil {
		return nil, ErrAccountsRequired
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
		apiKeys:            dependencies.APIKeys,
		accounts:           dependencies.Accounts,
		providerKeys:       dependencies.ProviderKeys,
		rateLimits:         dependencies.RateLimits,
		usage:              dependencies.Usage,
		providerOperations: dependencies.ProviderOperations,
		telemetry:          dependencies.Telemetry,
		tracing:            dependencies.Tracing,
		events:             dependencies.Events,
	}
	if teams := dependencies.Identity.Teams; teams != nil {
		// A key attributed to a team that no longer exists meters nothing:
		// the attribution outlived the team, and refusing such a key would
		// let a team deletion take its keys' traffic down.
		s.teamBudgets = func(ctx context.Context, teamID string) (*limits.TeamBudget, error) {
			record, err := teams.GetByID(ctx, teamID)
			if errors.Is(err, identity.ErrTeamNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return record.Team.Budget, nil
		}
	}
	s.authPolicy = authmode.NewPolicy(authmode.Setting{
		Mode:   config.AuthMode,
		Source: config.AuthModeSource,
	})
	s.auth = NewAuthMiddleware(s.apiKeys, s.accounts)
	s.auth.Govern(s.authPolicy, config.UnauthenticatedScopes)
	s.auth.AcceptSessions(dependencies.LocalGate)

	handlerConfig := controllers.Config{
		Service:            s.service,
		ProviderKeys:       s.providerKeys,
		APIKeys:            s.apiKeys,
		Accounts:           s.accounts,
		Usage:              dependencies.Usage,
		ProviderOperations: s.providerOperations,
		Catalog:            dependencies.Catalog,
		Presets:            dependencies.Presets,
		Templates:          dependencies.Templates,
		Files:              dependencies.Files,
		Jobs:               dependencies.Jobs,
		Batches:            dependencies.Batches,
		BatchGovernor:      s.batchGovernor(),
		FileUploadBound:    config.MaxFileUploadSize,
		FileBackend:        dependencies.FileBackend,
		ServiceName:        "starport",
		Build:              config.Build,
		Deployment:         dependencies.Deployment,
		Webhooks:           dependencies.Webhooks,
		AuthPolicy:         s.authPolicy,
		AuthModeStore:      config.AuthModeStore,
		AuthModeBindHost:   config.Host,
		AllowRemoteNoAuth:  config.AllowRemoteNoAuth,
		Console:            dependencies.Console,
		LocalGate:          dependencies.LocalGate,
		IdentityAuth:       dependencies.IdentityAuth,
		Identity:           dependencies.Identity,
		Audit:              dependencies.Audit,
		Events:             dependencies.Events,
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

func (s *Server) requireAccountAccess(next http.Handler) http.Handler {
	return s.auth.RequireAccountAccess(next)
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.auth.RequireAdmin(next)
}

// requireSwitchAccess guards the authentication switch.
//
// It is the admin scope whenever there is a key to hold. When authentication
// is disabled there is none: the gateway resolves every request to the
// anonymous key, which holds no admin scope on purpose, so requiring one
// would make the switch a one-way door — an operator could open the gateway
// and never close it again without editing configuration and restarting.
//
// What remains in that state is the controller's own guard, which is stricter
// than the admin scope in the direction that matters: the request has to come
// from this machine. So an open gateway can be locked from the machine that
// runs it, and cannot be opened further from anywhere else.
func (s *Server) requireSwitchAccess(next http.Handler) http.Handler {
	guarded := s.requireAdmin(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authPolicy.Disabled() {
			next.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
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
		Str("auth_mode", string(s.authPolicy.Current().Mode)).
		Str("auth_mode_source", string(s.authPolicy.Current().Source)).
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
