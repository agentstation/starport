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

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/tenant"
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

// SizeLimiter limits the size of request bodies
func SizeLimiter(maxSize int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only limit request body size for methods that have a body
			if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
				r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			}
			next.ServeHTTP(w, r)
		})
	}
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

// TenantReader reads one account by ID. The middleware holds this single
// method rather than the tenant repository, so the HTTP seam never learns how
// an account is stored or gains the power to write one.
type TenantReader interface {
	GetByID(ctx context.Context, id string) (tenant.Record, error)
}

// AuthMiddleware provides authentication functionality
type AuthMiddleware struct {
	identities identity.Repository
	tenants    TenantReader
}

// NewAuthMiddleware creates a new authentication middleware. The tenant reader
// is optional: without it a request still authenticates and runs under the
// default credential policy, because a key is a valid identity whether or not
// the deployment can read the account behind it.
func NewAuthMiddleware(identities identity.Repository, tenants ...TenantReader) *AuthMiddleware {
	middleware := &AuthMiddleware{identities: identities}
	if len(tenants) > 0 {
		middleware.tenants = tenants[0]
	}
	return middleware
}

// RequireAPIKey validates API key authentication
func (m *AuthMiddleware) RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract API key from headers. Query-string credentials are intentionally
		// unsupported because URLs are commonly logged by proxies and clients.
		apiKey := extractAPIKey(r)
		if apiKey == "" {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Missing API key")
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
		// The key authenticates. The tenant behind it decides what the request
		// may reach, so both travel and neither stands in for the other.
		tenantID := apiKeyModel.EffectiveTenantID()
		ctx = requestctx.WithTenantID(ctx, tenantID)
		if record, ok := m.readTenant(ctx, tenantID); ok {
			ctx = requestctx.WithTenantRecord(ctx, record)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// readTenant loads the account behind an authenticated key. A missing or
// unreadable account never fails the request: the key authenticated, and the
// governing policy falls back to the default. Refusing here would take a
// working deployment offline for a storage fault that has a safe default.
func (m *AuthMiddleware) readTenant(ctx context.Context, tenantID string) (*tenant.Tenant, bool) {
	if m.tenants == nil || tenantID == "" {
		return nil, false
	}
	record, err := m.tenants.GetByID(ctx, tenantID)
	if err != nil {
		if !errors.Is(err, tenant.ErrNotFound) {
			log.Error().Err(err).Str("tenant_id", tenantID).
				Msg("Failed to read the account behind an authenticated key")
		}
		return nil, false
	}
	governing := record.Tenant
	return &governing, true
}

// RequireTenantAccess guards a route addressed by account. A caller reaches
// its own account, and an operator holding admin reaches any account, because
// applying a credential on a tenant's behalf is a support operation an
// operator has to be able to perform. Nothing else passes.
//
// An operator naming an account that does not exist gets 404 rather than a
// silent write into a scope no tenant owns.
func (m *AuthMiddleware) RequireTenantAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeyModel, ok := requestctx.GetAPIKeyModel(r.Context())
		if !ok || apiKeyModel == nil {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Not authenticated")
			return
		}

		urlTenantID := chi.URLParam(r, "tenant_id")
		if urlTenantID == "" {
			writeProtocolError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing tenant ID")
			return
		}

		if urlTenantID == requestctx.TenantIDOrDefault(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}

		if !apiKeyModel.HasScope("admin") {
			writeProtocolError(w, r, http.StatusForbidden, "permission_error", "Access denied")
			return
		}

		// Without a tenant reader the deployment cannot tell a real account
		// from a typo, and the same rule as elsewhere applies: a key is a
		// valid identity whether or not accounts are readable.
		if m.tenants != nil {
			if _, err := m.tenants.GetByID(r.Context(), urlTenantID); err != nil {
				if errors.Is(err, tenant.ErrNotFound) {
					writeProtocolError(w, r, http.StatusNotFound, "not_found_error", "Tenant not found")
					return
				}
				log.Error().Err(err).Str("tenant_id", urlTenantID).
					Msg("Failed to read the account named by a credential route")
				writeProtocolError(w, r, http.StatusInternalServerError, "api_error", "Failed to read tenant")
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
