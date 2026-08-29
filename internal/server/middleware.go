package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server/requestctx"
)

// Context key type for middleware values.
type contextKey = requestctx.Key

// Context keys for middleware
const (
	ContextKeyAPIKey      contextKey = requestctx.APIKey
	ContextKeyAPIKeyID    contextKey = requestctx.APIKeyID
	ContextKeyAPIKeyModel contextKey = requestctx.APIKeyModel
)

// Middleware aliases for chi middleware.
var (
	RequestID = middleware.RequestID
	ClientIP  = middleware.ClientIPFromRemoteAddr
	Recoverer = middleware.Recoverer
	Compress  = middleware.Compress
)

// RequestLogger references the LoggingMiddleware from logger.go
// var RequestLogger = LoggingMiddleware (defined in logger.go)

// SecurityHeaders adds security headers to responses
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent content sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")

		// Enable XSS protection
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Prevent exposing referrer information
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Enforce HTTPS (optional - only if TLS is configured)
		// This is commented out by default since not all deployments use TLS
		// w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Content Security Policy - restrictive by default
		// This prevents loading resources from external sources
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none';")

		// Permissions Policy (formerly Feature Policy)
		// Disable features that aren't needed for an API
		w.Header().Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")

		next.ServeHTTP(w, r)
	})
}

// SizeLimiter limits the size of request bodies. A caller that states its
// size in Content-Length is refused before the gateway reads a byte of it,
// which matters most for the case this limit exists to catch: a request that
// carries attached media is large, and reading it only to discard it costs
// the memory the limit is there to protect.
//
// A body whose size the caller does not state is cut while it is read. The
// decode path answers that one, because only the reader learns the body was
// too long.
//
// The exempt predicate names the routes that carry their own bound. A file
// upload is one of them: this limit exists to stop a caller from making the
// gateway hold and decode a huge document, and an upload streams to a store
// instead. Its own bound is the operator's file setting, which is usually
// larger, so the general limit steps aside rather than clamping it lower.
func SizeLimiter(maxSize int64, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxSize <= 0 || !methodCarriesBody(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if exempt != nil && exempt(r) {
				next.ServeHTTP(w, r)
				return
			}
			if r.ContentLength > maxSize {
				writeRequestTooLarge(w, r, maxSize, r.ContentLength)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			next.ServeHTTP(w, r)
		})
	}
}

// methodCarriesBody reports whether a method can carry a request body worth
// limiting.
func methodCarriesBody(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
}

// Timeout adds a timeout to requests
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	if timeout <= 0 {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return middleware.Timeout(timeout)
}

// CORS returns a configured CORS handler
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	return cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   cfg.AllowedMethods,
		AllowedHeaders:   cfg.AllowedHeaders,
		ExposedHeaders:   cfg.ExposedHeaders,
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           cfg.MaxAge,
	})
}

// AccountReader reads one account by ID. The middleware holds this single
// method rather than the account repository, so the HTTP seam never learns how
// an account is stored or gains the power to write one.
type AccountReader interface {
	GetByID(ctx context.Context, id string) (account.Record, error)
}

// AuthMiddleware provides authentication functionality
type AuthMiddleware struct {
	identities identity.Repository
	accounts   AccountReader
	// policy is the live authentication mode. It is read once per request, not
	// once per router build, because the console can change the mode without a
	// restart and "disabled" must not come to mean "disabled at boot". A nil
	// policy reports required, which is what a zero-valued middleware has to
	// mean.
	policy *authmode.Policy
	// anonymous is the identity a request runs as while the mode is disabled.
	// The scope set is the operator's and does not change at runtime, so it is
	// resolved once.
	anonymous identity.APIKey
	// sessions verifies console sessions opened by `starport ui`. A nil gate
	// means this gateway has no local admin token to check them against, so
	// every session cookie is refused and only bearer keys authenticate.
	sessions *localauth.Gate
}

// NewAuthMiddleware creates a new authentication middleware. The account reader
// is optional: without it a request still authenticates and runs under the
// default credential policy, because a key is a valid identity whether or not
// the deployment can read the account behind it.
func NewAuthMiddleware(identities identity.Repository, accounts ...AccountReader) *AuthMiddleware {
	middleware := &AuthMiddleware{identities: identities}
	if len(accounts) > 0 {
		middleware.accounts = accounts[0]
	}
	return middleware
}

// Govern binds the live authentication policy and the identity a request runs
// as while that policy is disabled. An empty scope list means
// identity.DefaultAnonymousScopes.
//
// The operator decides both; nothing in a request can. Passing them together
// is the point: a policy without an anonymous identity would disable the key
// check and leave every downstream seam without a subject to meter.
func (m *AuthMiddleware) Govern(policy *authmode.Policy, scopes []string) {
	m.policy = policy
	m.anonymous = identity.Anonymous(scopes)
}

// AcceptSessions binds the gate that verifies console sessions.
//
// It is separate from Govern because the two answer different questions. The
// policy decides whether a credential is required at all; the gate decides
// whether one particular kind of credential is genuine. A deployment can have
// either without the other.
func (m *AuthMiddleware) AcceptSessions(gate *localauth.Gate) {
	m.sessions = gate
}

// RequireAPIKey validates API key authentication
func (m *AuthMiddleware) RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.policy.Disabled() {
			next.ServeHTTP(w, r.WithContext(m.anonymousContext(r.Context())))
			return
		}

		// Extract API key from headers. Query-string credentials are intentionally
		// unsupported because URLs are commonly logged by proxies and clients.
		apiKey := extractAPIKey(r)
		if apiKey == "" {
			// A browser that was opened by `starport ui` carries a console session
			// instead of a key. It is read only when no key was presented, because
			// an Authorization header is something a caller chose to send and a
			// cookie is something the browser attached on its own; the explicit
			// credential decides.
			ctx, err := m.sessionContext(r)
			switch {
			case err == nil:
				next.ServeHTTP(w, r.WithContext(ctx))
			case errors.Is(err, errNoSession):
				writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Missing API key")
			default:
				writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", sessionRefusal)
			}
			return
		}

		// Hash the provided API key
		hash := sha256.Sum256([]byte(apiKey))
		hashStr := hex.EncodeToString(hash[:])

		record, err := m.identities.GetByHash(r.Context(), hashStr)
		if err != nil {
			if errors.Is(err, identity.ErrNotFound) {
				writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Invalid API key")
				return
			}
			log.Error().Err(err).Msg("Failed to lookup API key hash")
			writeProtocolError(w, r, http.StatusInternalServerError, "server_error", "Authentication error")
			return
		}

		apiKeyModel := record.APIKey

		// Check if key is active
		if !apiKeyModel.Active {
			writeProtocolError(w, r, http.StatusForbidden, "permission_error", "API key is disabled")
			return
		}

		if apiKeyModel.IsExpired() {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "API key has expired")
			return
		}

		// Add API key info to context
		ctx := requestctx.WithAPIKey(r.Context(), apiKey)
		ctx = requestctx.WithAPIKeyID(ctx, apiKeyModel.ID)
		ctx = requestctx.WithAPIKeyModel(ctx, &apiKeyModel)
		// The key authenticates. The account behind it decides what the request
		// may reach, so both travel and neither stands in for the other.
		accountID := apiKeyModel.EffectiveAccountID()
		ctx = requestctx.WithAccountID(ctx, accountID)
		if record, ok := m.readAccount(ctx, accountID); ok {
			ctx = requestctx.WithAccountRecord(ctx, record)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// anonymousContext furnishes the request with the same identity every
// authenticated request carries, minus the key. Rate limits, budgets, usage
// records, and scope checks all read the context and none of them learn that
// authentication was off, so an unauthenticated deployment is metered and
// governed exactly like an authenticated one.
//
// It deliberately does not call requestctx.WithAPIKey: there is no secret, and
// putting a made-up one in the context would let a downstream reader believe a
// key was presented. A request that did present a key is treated no
// differently — disabling authentication turns the check off rather than
// making it optional, so a stale or mistyped key cannot quietly move a caller
// onto another account's limits and credentials.
func (m *AuthMiddleware) anonymousContext(ctx context.Context) context.Context {
	anonymous := m.anonymous
	ctx = requestctx.WithAPIKeyID(ctx, anonymous.ID)
	ctx = requestctx.WithAPIKeyModel(ctx, &anonymous)
	accountID := anonymous.EffectiveAccountID()
	ctx = requestctx.WithAccountID(ctx, accountID)
	if record, ok := m.readAccount(ctx, accountID); ok {
		ctx = requestctx.WithAccountRecord(ctx, record)
	}
	return ctx
}

// errNoSession reports a request that presented no console session at all, as
// opposed to one that presented a session this gateway will not accept. The
// two get different answers: the first caller holds no session, and the second
// has a cookie to replace.
var errNoSession = errors.New("no console session was presented")

// sessionRefusal is the answer to every unusable session, whatever made it
// unusable. A session goes stale on its own — it expires, or `starport auth
// rotate` invalidates it — so the message names the way back in rather than the
// cause, and telling a caller which of the two it was would say whether their
// cookie had ever been real.
const sessionRefusal = "This console session is no longer valid. Run `starport ui` to open a new one"

// sessionContext furnishes a request that arrived with a console session.
//
// The session runs as the local operator, holding every scope. Opening one
// required reading a file only this machine's account can read, so the holder
// is someone who can already run the CLI, read the database, and edit the
// configuration; granting them less here would withhold no power and only send
// them to a terminal to do the same work.
//
// It deliberately does not call requestctx.WithAPIKey. There is no gateway API
// key in play, and putting the cookie there would hand a credential to every
// downstream reader that treats that value as a bearer token to forward.
func (m *AuthMiddleware) sessionContext(r *http.Request) (context.Context, error) {
	cookie, err := r.Cookie(localauth.SessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, errNoSession
	}
	if _, err := m.sessions.Verify(cookie.Value, time.Now()); err != nil {
		return nil, err
	}
	operator := identity.LocalOperator()
	ctx := requestctx.WithAPIKeyID(r.Context(), operator.ID)
	ctx = requestctx.WithAPIKeyModel(ctx, &operator)
	accountID := operator.EffectiveAccountID()
	ctx = requestctx.WithAccountID(ctx, accountID)
	if record, ok := m.readAccount(ctx, accountID); ok {
		ctx = requestctx.WithAccountRecord(ctx, record)
	}
	return ctx, nil
}

// readAccount loads the account behind an authenticated key. A missing or
// unreadable account never fails the request: the key authenticated, and the
// governing policy falls back to the default. Refusing here would take a
// working deployment offline for a storage fault that has a safe default.
func (m *AuthMiddleware) readAccount(ctx context.Context, accountID string) (*account.Account, bool) {
	if m.accounts == nil || accountID == "" {
		return nil, false
	}
	record, err := m.accounts.GetByID(ctx, accountID)
	if err != nil {
		if !errors.Is(err, account.ErrNotFound) {
			log.Error().Err(err).Str("account_id", accountID).
				Msg("Failed to read the account behind an authenticated key")
		}
		return nil, false
	}
	governing := record.Account
	return &governing, true
}

// RequireAccountAccess guards a route addressed by account. A caller reaches
// its own account, and an operator holding admin reaches any account, because
// applying a credential on an account's behalf is a support operation an
// operator has to be able to perform. Nothing else passes.
//
// An operator naming an account that does not exist gets 404 rather than a
// silent write into a scope no account owns.
func (m *AuthMiddleware) RequireAccountAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeyModel, ok := requestctx.GetAPIKeyModel(r.Context())
		if !ok || apiKeyModel == nil {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Not authenticated")
			return
		}

		urlAccountID := chi.URLParam(r, "account_id")
		if urlAccountID == "" {
			writeProtocolError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing account ID")
			return
		}

		if urlAccountID == requestctx.AccountIDOrDefault(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}

		if !apiKeyModel.HasScope("admin") {
			writeProtocolError(w, r, http.StatusForbidden, "permission_error", "Access denied")
			return
		}

		// Without an account reader the deployment cannot tell a real account
		// from a typo, and the same rule as elsewhere applies: a key is a
		// valid identity whether or not accounts are readable.
		if m.accounts != nil {
			if _, err := m.accounts.GetByID(r.Context(), urlAccountID); err != nil {
				if errors.Is(err, account.ErrNotFound) {
					writeProtocolError(w, r, http.StatusNotFound, "not_found_error", "Account not found")
					return
				}
				log.Error().Err(err).Str("account_id", urlAccountID).
					Msg("Failed to read the account named by a credential route")
				writeProtocolError(w, r, http.StatusInternalServerError, "api_error", "Failed to read account")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// RequireAdmin validates admin privileges
func (m *AuthMiddleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get API key model from context
		apiKeyModel, ok := requestctx.GetAPIKeyModel(r.Context())
		if !ok {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Not authenticated")
			return
		}

		if !apiKeyModel.HasScope("admin") {
			writeProtocolError(w, r, http.StatusForbidden, "permission_error", "Admin access required")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RequireAnyScope validates that the authenticated API key has at least one
// accepted scope. The wildcard "*" grants access to all scopes.
func (m *AuthMiddleware) RequireAnyScope(scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKeyModel, ok := requestctx.GetAPIKeyModel(r.Context())
			if !ok || apiKeyModel == nil {
				writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Not authenticated")
				return
			}

			for _, scope := range scopes {
				if apiKeyModel.HasScope(scope) {
					next.ServeHTTP(w, r)
					return
				}
			}

			writeProtocolError(w, r, http.StatusForbidden, "permission_error", "Insufficient API key scope")
		})
	}
}

// extractAPIKey extracts the API key from the request
func extractAPIKey(r *http.Request) string {
	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if auth != "" {
		// Bearer token
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
		// Direct key
		return auth
	}

	// Check X-API-Key header
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}

	return ""
}
