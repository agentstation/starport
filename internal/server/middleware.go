package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/apikeys"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/storage"
)

// Context key type for middleware values
type contextKey string

// Context keys for middleware
const (
	ContextKeyAPIKey      contextKey = "api_key"
	ContextKeyAPIKeyID    contextKey = "api_key_id"
	ContextKeyAPIKeyModel contextKey = "api_key_model" // #nosec G101 - not a credential, just a context key identifier
)

// Middleware aliases for chi middleware
var (
	RequestID = middleware.RequestID
	RealIP    = middleware.RealIP
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

// RequestSizeLimiter is an alias for SizeLimiter for backward compatibility
func RequestSizeLimiter(maxBytes int64) func(http.Handler) http.Handler {
	return SizeLimiter(maxBytes)
}

// Timeout adds a timeout to requests
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no timeout is set, pass through without timeout
			if timeout <= 0 {
				next.ServeHTTP(w, r)
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			done := make(chan struct{})
			panicChan := make(chan any, 1)

			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicChan <- p
					}
				}()

				r = r.WithContext(ctx)
				next.ServeHTTP(w, r)
				close(done)
			}()

			select {
			case <-done:
				// Request completed normally
			case p := <-panicChan:
				// Re-panic to let the recoverer handle it
				panic(p)
			case <-ctx.Done():
				// Timeout occurred
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = w.Write([]byte("Request timeout"))
			}
		})
	}
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
	store storage.KVStore
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(store storage.KVStore) *AuthMiddleware {
	return &AuthMiddleware{
		store: store,
	}
}

// RequireAPIKey validates API key authentication
func (m *AuthMiddleware) RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract API key from header or query parameter
		apiKey := extractAPIKey(r)
		if apiKey == "" {
			dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeAuthenticationError, "Missing API key")
			return
		}

		// Hash the provided API key
		hash := sha256.Sum256([]byte(apiKey))
		hashStr := hex.EncodeToString(hash[:])

		// Look up the key ID by hash
		keyIDData, err := m.store.Get(r.Context(), storage.APIKeyHashKey(hashStr))
		if err != nil {
			if err == storage.ErrNotFound {
				dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeAuthenticationError, "Invalid API key")
				return
			}
			log.Error().Err(err).Msg("Failed to lookup API key hash")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Authentication error")
			return
		}

		keyID := string(keyIDData)

		// Get the full API key data by ID
		keyData, err := m.store.Get(r.Context(), storage.APIKeyKey(keyID))
		if err != nil {
			if err == storage.ErrNotFound {
				dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeAuthenticationError, "Invalid API key")
				return
			}
			log.Error().Err(err).Msg("Failed to validate API key")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Authentication error")
			return
		}

		// Deserialize API key
		var apiKeyModel apikeys.APIKey
		if err := storage.Deserialize(keyData, &apiKeyModel); err != nil {
			log.Error().Err(err).Msg("Failed to deserialize API key")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Authentication error")
			return
		}

		// Verify the hash matches (extra security check)
		if apiKeyModel.Hash != hashStr {
			log.Error().
				Str("expected_hash", apiKeyModel.Hash).
				Str("actual_hash", hashStr).
				Msg("API key hash mismatch")
			dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeAuthenticationError, "Invalid API key")
			return
		}

		// Check if key is active
		if !apiKeyModel.Active {
			dto.WriteError(w, http.StatusForbidden, dto.ErrorTypePermissionError, "API key is disabled")
			return
		}

		// Add API key info to context
		ctx := context.WithValue(r.Context(), ContextKeyAPIKey, apiKey)
		ctx = context.WithValue(ctx, ContextKeyAPIKeyID, apiKeyModel.ID)
		ctx = context.WithValue(ctx, ContextKeyAPIKeyModel, &apiKeyModel)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireKeyOwnership validates that the user owns the API key they're trying to manage
func (m *AuthMiddleware) RequireKeyOwnership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get authenticated key ID from context
		authKeyID, ok := r.Context().Value(ContextKeyAPIKeyID).(string)
		if !ok {
			dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeAuthenticationError, "Not authenticated")
			return
		}

		// Get key ID from URL
		urlKeyID := chi.URLParam(r, "key_id")
		if urlKeyID == "" {
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Missing key ID")
			return
		}

		// Check ownership
		if authKeyID != urlKeyID {
			dto.WriteError(w, http.StatusForbidden, dto.ErrorTypePermissionError, "Access denied")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RequireAdmin validates admin privileges
func (m *AuthMiddleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get API key model from context
		apiKeyModel, ok := r.Context().Value(ContextKeyAPIKeyModel).(*apikeys.APIKey)
		if !ok {
			dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeAuthenticationError, "Not authenticated")
			return
		}

		// Check for admin scope
		hasAdmin := false
		for _, scope := range apiKeyModel.Scopes {
			if scope == "admin" || scope == "*" {
				hasAdmin = true
				break
			}
		}

		if !hasAdmin {
			dto.WriteError(w, http.StatusForbidden, dto.ErrorTypePermissionError, "Admin access required")
			return
		}

		next.ServeHTTP(w, r)
	})
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

	// Check query parameter
	if key := r.URL.Query().Get("api_key"); key != "" {
		return key
	}

	// Check alternative query parameter
	if key := r.URL.Query().Get("key"); key != "" {
		return key
	}

	return ""
}
