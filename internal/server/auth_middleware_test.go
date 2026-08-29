package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/storage"
)

func TestAuthMiddleware_RequireAPIKey(t *testing.T) {
	expiresAt := time.Now().Add(-time.Hour)
	tests := []struct {
		name           string
		setupAuth      func(req *http.Request)
		storedSecret   string
		storedIdentity *apikey.APIKey
		wantStatus     int
		wantErrMessage string
		wantContext    bool
	}{
		{
			name: "missing API key",
			setupAuth: func(req *http.Request) {
				// Don't set any auth header
			},
			wantStatus:     http.StatusUnauthorized,
			wantErrMessage: "Missing API key",
			wantContext:    false,
		},
		{
			name: "invalid API key - not found",
			setupAuth: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer invalid-key")
			},
			wantStatus:     http.StatusUnauthorized,
			wantErrMessage: "Invalid API key",
			wantContext:    false,
		},
		{
			name: "valid API key via Bearer token",
			setupAuth: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-starport-testkey123")
			},
			storedSecret: "sk-starport-testkey123",
			storedIdentity: &apikey.APIKey{
				ID:        "STARPORT_test123",
				Name:      "Test-Key",
				Scopes:    []string{"chat:write", "models:read"},
				Active:    true,
				CreatedAt: time.Now(),
			},
			wantStatus:  http.StatusOK,
			wantContext: true,
		},
		{
			name: "valid API key via X-API-Key header",
			setupAuth: func(req *http.Request) {
				req.Header.Set("X-API-Key", "sk-starport-testkey456")
			},
			storedSecret: "sk-starport-testkey456",
			storedIdentity: &apikey.APIKey{
				ID:        "STARPORT_test456",
				Name:      "Test-Key-2",
				Scopes:    []string{"chat:write"},
				Active:    true,
				CreatedAt: time.Now(),
			},
			wantStatus:  http.StatusOK,
			wantContext: true,
		},
		{
			name: "reject API key via query parameter",
			setupAuth: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("api_key", "sk-starport-testkey789")
				req.URL.RawQuery = q.Encode()
			},
			wantStatus:     http.StatusUnauthorized,
			wantErrMessage: "Missing API key",
			wantContext:    false,
		},
		{
			name: "reject API key via key query parameter",
			setupAuth: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("key", "sk-starport-keytest999")
				req.URL.RawQuery = q.Encode()
			},
			wantStatus:     http.StatusUnauthorized,
			wantErrMessage: "Missing API key",
			wantContext:    false,
		},
		{
			name: "disabled API key",
			setupAuth: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-starport-disabled")
			},
			storedSecret: "sk-starport-disabled",
			storedIdentity: &apikey.APIKey{
				ID:        "STARPORT_disabled",
				Name:      "Disabled-Key",
				Scopes:    []string{"chat:write"},
				Active:    false,
				CreatedAt: time.Now(),
			},
			wantStatus:     http.StatusForbidden,
			wantErrMessage: "API key is disabled",
			wantContext:    false,
		},
		{
			name: "expired API key",
			setupAuth: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-starport-expired")
			},
			storedSecret: "sk-starport-expired",
			storedIdentity: &apikey.APIKey{
				ID:        "STARPORT_expired",
				Name:      "Expired-Key",
				Scopes:    []string{"chat:write"},
				Active:    true,
				CreatedAt: time.Now().Add(-2 * time.Hour),
				ExpiresAt: &expiresAt,
			},
			wantStatus:     http.StatusUnauthorized,
			wantErrMessage: "API key has expired",
			wantContext:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			identities, err := apikey.Open(storage.NewMockStore())
			require.NoError(t, err)
			if tt.storedIdentity != nil {
				storedIdentity := *tt.storedIdentity
				hash := sha256.Sum256([]byte(tt.storedSecret))
				storedIdentity.Hash = hex.EncodeToString(hash[:])
				_, err := identities.Create(context.Background(), storedIdentity)
				require.NoError(t, err)
			}

			auth := NewAuthMiddleware(identities)

			// Create a test handler that checks context
			var gotContext bool
			var gotAPIKey string
			var gotAPIKeyID string
			var gotAPIKeyModel *apikey.APIKey

			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotContext = true
				if key, ok := r.Context().Value(ContextKeyAPIKey).(string); ok {
					gotAPIKey = key
				}
				if id, ok := r.Context().Value(ContextKeyAPIKeyID).(string); ok {
					gotAPIKeyID = id
				}
				if model, ok := r.Context().Value(ContextKeyAPIKeyModel).(*apikey.APIKey); ok {
					gotAPIKeyModel = model
				}
				w.WriteHeader(http.StatusOK)
			})

			// Wrap with auth middleware
			handler := auth.RequireAPIKey(testHandler)

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.setupAuth != nil {
				tt.setupAuth(req)
			}

			// Execute
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			// Assert
			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantErrMessage != "" {
				assert.Contains(t, rec.Body.String(), tt.wantErrMessage)
			}

			if tt.wantContext {
				assert.True(t, gotContext, "handler should have been called")
				assert.NotEmpty(t, gotAPIKey, "API key should be in context")
				assert.NotEmpty(t, gotAPIKeyID, "API key ID should be in context")
				assert.NotNil(t, gotAPIKeyModel, "API key model should be in context")
			} else {
				assert.False(t, gotContext, "handler should not have been called")
			}
		})
	}
}

func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		setupReq func(req *http.Request)
		wantKey  string
	}{
		{
			name: "Bearer token",
			setupReq: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-test-123")
			},
			wantKey: "sk-test-123",
		},
		{
			name: "Direct authorization header",
			setupReq: func(req *http.Request) {
				req.Header.Set("Authorization", "sk-test-456")
			},
			wantKey: "sk-test-456",
		},
		{
			name: "X-API-Key header",
			setupReq: func(req *http.Request) {
				req.Header.Set("X-API-Key", "sk-test-789")
			},
			wantKey: "sk-test-789",
		},
		{
			name: "Ignore query parameter api_key",
			setupReq: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("api_key", "sk-test-query")
				req.URL.RawQuery = q.Encode()
			},
			wantKey: "",
		},
		{
			name: "Ignore query parameter key",
			setupReq: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("key", "sk-test-key")
				req.URL.RawQuery = q.Encode()
			},
			wantKey: "",
		},
		{
			name: "No API key",
			setupReq: func(req *http.Request) {
				// Don't set anything
			},
			wantKey: "",
		},
		{
			name: "Prefer Bearer over other methods",
			setupReq: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-bearer")
				req.Header.Set("X-API-Key", "sk-x-api-key")
				q := req.URL.Query()
				q.Set("api_key", "sk-query")
				req.URL.RawQuery = q.Encode()
			},
			wantKey: "sk-bearer",
		},
		{
			name: "Ignore query parameters without headers",
			setupReq: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("api_key", "sk-api-key-param")
				q.Set("key", "sk-key-param")
				req.URL.RawQuery = q.Encode()
			},
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.setupReq != nil {
				tt.setupReq(req)
			}

			gotKey := extractAPIKey(req)
			assert.Equal(t, tt.wantKey, gotKey)
		})
	}
}

func TestAuthMiddleware_RequireAnyScope(t *testing.T) {
	identities, err := apikey.Open(storage.NewMockStore())
	require.NoError(t, err)
	auth := NewAuthMiddleware(identities)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := auth.RequireAnyScope("chat:write", "embeddings:write")(next)

	tests := []struct {
		name       string
		apiKey     *apikey.APIKey
		wantStatus int
	}{
		{
			name:       "allowed explicit scope",
			apiKey:     &apikey.APIKey{Scopes: []string{"chat:write"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "allowed wildcard scope",
			apiKey:     &apikey.APIKey{Scopes: []string{"*"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "denied missing scope",
			apiKey:     &apikey.APIKey{Scopes: []string{"models:read"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "denied missing authentication context",
			apiKey:     nil,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.apiKey != nil {
				req = req.WithContext(context.WithValue(req.Context(), ContextKeyAPIKeyModel, tt.apiKey))
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

// TestAuthIntegrationWithConsole tests the full flow of generating a key via the console
// and then using it for authentication
func TestAuthIntegrationWithConsole(t *testing.T) {
	// This test simulates the full flow:
	// 1. The console generates a key
	// 2. The key is stored with proper hash mapping
	// 3. The key can be used for authentication

	identities, err := apikey.Open(storage.NewMockStore())
	require.NoError(t, err)

	// Simulate what the console does when generating a key
	keyValue := "test-starport-integration-key"
	keyID := "STARPORT_integration123"

	// Hash the key
	hash := sha256.Sum256([]byte(keyValue))
	hashStr := hex.EncodeToString(hash[:])

	// Create API key model
	apiKey := &apikey.APIKey{
		ID:        keyID,
		Name:      "Console-Integration-Test-Key",
		Hash:      hashStr,
		Scopes:    []string{"chat:write", "models:read"},
		Active:    true,
		CreatedAt: time.Now(),
		Metadata: map[string]any{
			"source": "console",
		},
	}

	// Store the key (what the console handler does)
	ctx := context.Background()
	_, err = identities.Create(ctx, *apiKey)
	require.NoError(t, err)

	// Now test authentication with this key
	auth := NewAuthMiddleware(identities)

	// Create a test handler
	authenticated := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated = true

		// Verify context values
		ctxKey := r.Context().Value(ContextKeyAPIKey).(string)
		assert.Equal(t, keyValue, ctxKey)

		ctxKeyID := r.Context().Value(ContextKeyAPIKeyID).(string)
		assert.Equal(t, keyID, ctxKeyID)

		ctxModel := r.Context().Value(ContextKeyAPIKeyModel).(*apikey.APIKey)
		assert.Equal(t, apiKey.Name, ctxModel.Name)
		assert.Equal(t, apiKey.Scopes, ctxModel.Scopes)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("authenticated"))
	})

	// Wrap with auth middleware
	handler := auth.RequireAPIKey(testHandler)

	// Make request with the generated key
	req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+keyValue)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Assert successful authentication
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, authenticated, "request should have been authenticated")
	assert.Equal(t, "authenticated", rec.Body.String())
}
