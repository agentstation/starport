package server

import (
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

// AuthMiddleware provides authentication functionality
type AuthMiddleware struct {
	identities identity.Repository
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(identities identity.Repository) *AuthMiddleware {
	return &AuthMiddleware{
		identities: identities,
	}
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
		ctx = requestctx.WithTenantID(ctx, apiKeyModel.EffectiveTenantID())

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireKeyOwnership validates that the user owns the API key they're trying to manage
func (m *AuthMiddleware) RequireKeyOwnership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get authenticated key ID from context
		authKeyID, ok := requestctx.GetAPIKeyID(r.Context())
		if !ok {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Not authenticated")
			return
		}

		// Get key ID from URL
		urlKeyID := chi.URLParam(r, "key_id")
		if urlKeyID == "" {
			writeProtocolError(w, r, http.StatusBadRequest, "invalid_request_error", "Missing key ID")
			return
		}

		// Check ownership
		if authKeyID != urlKeyID {
			writeProtocolError(w, r, http.StatusForbidden, "permission_error", "Access denied")
			return
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
