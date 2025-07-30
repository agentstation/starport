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

	"github.com/agentstation/starport/internal/apikeys"
	"github.com/agentstation/starport/internal/storage"
)

func TestAuthMiddleware_RequireAPIKey(t *testing.T) {
	tests := []struct {
		name           string
		setupAuth      func(req *http.Request)
		setupStore     func(store *storage.MockStore)
		wantStatus     int
		wantErrMessage string
		wantContext    bool
	}{
		{
			name: "missing API key",
			setupAuth: func(req *http.Request) {
				// Don't set any auth header
			},
			setupStore: func(store *storage.MockStore) {
				// No setup needed
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
			setupStore: func(store *storage.MockStore) {
				// Don't store anything - the key lookup will naturally fail
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
			setupStore: func(store *storage.MockStore) {
				keyValue := "sk-starport-testkey123"
				keyID := "STARPORT_test123"

				// Hash the key
				hash := sha256.Sum256([]byte(keyValue))
				hashStr := hex.EncodeToString(hash[:])

				// Store hash -> ID mapping
				store.Set(context.Background(), storage.APIKeyHashKey(hashStr), []byte(keyID))

				// Store the API key
				apiKey := &apikeys.APIKey{
					ID:        keyID,
					Name:      "Test Key",
					Hash:      hashStr,
					Scopes:    []string{"chat:write", "models:read"},
					Active:    true,
					CreatedAt: time.Now(),
				}
				keyData, _ := storage.Serialize(apiKey)
				store.Set(context.Background(), storage.APIKeyKey(keyID), keyData)
			},
			wantStatus:  http.StatusOK,
			wantContext: true,
		},
		{
			name: "valid API key via X-API-Key header",
			setupAuth: func(req *http.Request) {
				req.Header.Set("X-API-Key", "sk-starport-testkey456")
			},
			setupStore: func(store *storage.MockStore) {
				keyValue := "sk-starport-testkey456"
				keyID := "STARPORT_test456"

				// Hash the key
				hash := sha256.Sum256([]byte(keyValue))
				hashStr := hex.EncodeToString(hash[:])

				// Store hash -> ID mapping
				store.Set(context.Background(), storage.APIKeyHashKey(hashStr), []byte(keyID))

				// Store the API key
				apiKey := &apikeys.APIKey{
					ID:        keyID,
					Name:      "Test Key 2",
					Hash:      hashStr,
					Scopes:    []string{"chat:write"},
					Active:    true,
					CreatedAt: time.Now(),
				}
				keyData, _ := storage.Serialize(apiKey)
				store.Set(context.Background(), storage.APIKeyKey(keyID), keyData)
			},
			wantStatus:  http.StatusOK,
			wantContext: true,
		},
		{
			name: "valid API key via query parameter",
			setupAuth: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("api_key", "sk-starport-testkey789")
				req.URL.RawQuery = q.Encode()
			},
			setupStore: func(store *storage.MockStore) {
				keyValue := "sk-starport-testkey789"
				keyID := "STARPORT_test789"

				// Hash the key
				hash := sha256.Sum256([]byte(keyValue))
				hashStr := hex.EncodeToString(hash[:])

				// Store hash -> ID mapping
				store.Set(context.Background(), storage.APIKeyHashKey(hashStr), []byte(keyID))

				// Store the API key
				apiKey := &apikeys.APIKey{
					ID:        keyID,
					Name:      "Test Key 3",
					Hash:      hashStr,
					Scopes:    []string{"models:read"},
					Active:    true,
					CreatedAt: time.Now(),
				}
				keyData, _ := storage.Serialize(apiKey)
				store.Set(context.Background(), storage.APIKeyKey(keyID), keyData)
			},
			wantStatus:  http.StatusOK,
			wantContext: true,
		},
		{
			name: "valid API key via key query parameter",
			setupAuth: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("key", "sk-starport-keytest999")
				req.URL.RawQuery = q.Encode()
			},
			setupStore: func(store *storage.MockStore) {
				keyValue := "sk-starport-keytest999"
				keyID := "STARPORT_keytest999"
				// Hash the key
				hash := sha256.Sum256([]byte(keyValue))
				hashStr := hex.EncodeToString(hash[:])
				// Store hash -> ID mapping
				store.Set(context.Background(), storage.APIKeyHashKey(hashStr), []byte(keyID))
				// Store the API key
				apiKey := &apikeys.APIKey{
					ID:        keyID,
					Name:      "Key Test Key",
					Hash:      hashStr,
					Scopes:    []string{"models:read"},
					Active:    true,
					CreatedAt: time.Now(),
				}
				keyData, _ := storage.Serialize(apiKey)
				store.Set(context.Background(), storage.APIKeyKey(keyID), keyData)
			},
			wantStatus:  http.StatusOK,
			wantContext: true,
		},
		{
			name: "disabled API key",
			setupAuth: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-starport-disabled")
			},
			setupStore: func(store *storage.MockStore) {
				keyValue := "sk-starport-disabled"
				keyID := "STARPORT_disabled"

				// Hash the key
				hash := sha256.Sum256([]byte(keyValue))
				hashStr := hex.EncodeToString(hash[:])

				// Store hash -> ID mapping
				store.Set(context.Background(), storage.APIKeyHashKey(hashStr), []byte(keyID))

				// Store the API key (disabled)
				apiKey := &apikeys.APIKey{
					ID:        keyID,
					Name:      "Disabled Key",
					Hash:      hashStr,
					Scopes:    []string{"chat:write"},
					Active:    false, // Disabled
					CreatedAt: time.Now(),
				}
				keyData, _ := storage.Serialize(apiKey)
				store.Set(context.Background(), storage.APIKeyKey(keyID), keyData)
			},
			wantStatus:     http.StatusForbidden,
			wantErrMessage: "API key is disabled",
			wantContext:    false,
		},
		{
			name: "hash mismatch",
			setupAuth: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer sk-starport-mismatch")
			},
			setupStore: func(store *storage.MockStore) {
				keyValue := "sk-starport-mismatch"
				keyID := "STARPORT_mismatch"

				// Hash the key
				hash := sha256.Sum256([]byte(keyValue))
				hashStr := hex.EncodeToString(hash[:])

				// Store hash -> ID mapping
				store.Set(context.Background(), storage.APIKeyHashKey(hashStr), []byte(keyID))

				// Store the API key with WRONG hash
				apiKey := &apikeys.APIKey{
					ID:        keyID,
					Name:      "Mismatch Key",
					Hash:      "wronghash",
					Scopes:    []string{"chat:write"},
					Active:    true,
					CreatedAt: time.Now(),
				}
				keyData, _ := storage.Serialize(apiKey)
				store.Set(context.Background(), storage.APIKeyKey(keyID), keyData)
			},
			wantStatus:     http.StatusUnauthorized,
			wantErrMessage: "Invalid API key",
			wantContext:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			store := storage.NewMockStore()
			if tt.setupStore != nil {
				tt.setupStore(store)
			}

			auth := NewAuthMiddleware(store)

			// Create a test handler that checks context
			var gotContext bool
			var gotAPIKey string
			var gotAPIKeyID string
			var gotAPIKeyModel *apikeys.APIKey

			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotContext = true
				if key, ok := r.Context().Value(ContextKeyAPIKey).(string); ok {
					gotAPIKey = key
				}
				if id, ok := r.Context().Value(ContextKeyAPIKeyID).(string); ok {
					gotAPIKeyID = id
				}
				if model, ok := r.Context().Value(ContextKeyAPIKeyModel).(*apikeys.APIKey); ok {
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
			name: "Query parameter api_key",
			setupReq: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("api_key", "sk-test-query")
				req.URL.RawQuery = q.Encode()
			},
			wantKey: "sk-test-query",
		},
		{
			name: "Query parameter key",
			setupReq: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("key", "sk-test-key")
				req.URL.RawQuery = q.Encode()
			},
			wantKey: "sk-test-key",
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
			name: "Prefer api_key over key query parameter",
			setupReq: func(req *http.Request) {
				q := req.URL.Query()
				q.Set("api_key", "sk-api-key-param")
				q.Set("key", "sk-key-param")
				req.URL.RawQuery = q.Encode()
			},
			wantKey: "sk-api-key-param",
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

// TestAuthIntegrationWithChatUI tests the full flow of generating a key via ChatUI
// and then using it for authentication
func TestAuthIntegrationWithChatUI(t *testing.T) {
	// This test simulates the full flow:
	// 1. ChatUI generates a key
	// 2. The key is stored with proper hash mapping
	// 3. The key can be used for authentication

	store := storage.NewMockStore()

	// Simulate what ChatUI does when generating a key
	keyValue := "sk-starport-integrationtest123"
	keyID := "STARPORT_integration123"

	// Hash the key
	hash := sha256.Sum256([]byte(keyValue))
	hashStr := hex.EncodeToString(hash[:])

	// Create API key model
	apiKey := &apikeys.APIKey{
		ID:        keyID,
		Name:      "ChatUI Integration Test Key",
		Hash:      hashStr,
		Scopes:    []string{"chat:write", "models:read"},
		Active:    true,
		CreatedAt: time.Now(),
		Metadata: map[string]any{
			"source": "chatui",
		},
	}

	// Store the key (what ChatUI handler does)
	ctx := context.Background()
	keyData, err := storage.Serialize(apiKey)
	require.NoError(t, err)

	err = store.Set(ctx, storage.APIKeyKey(keyID), keyData)
	require.NoError(t, err)

	err = store.Set(ctx, storage.APIKeyHashKey(hashStr), []byte(keyID))
	require.NoError(t, err)

	// Now test authentication with this key
	auth := NewAuthMiddleware(store)

	// Create a test handler
	authenticated := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated = true

		// Verify context values
		ctxKey := r.Context().Value(ContextKeyAPIKey).(string)
		assert.Equal(t, keyValue, ctxKey)

		ctxKeyID := r.Context().Value(ContextKeyAPIKeyID).(string)
		assert.Equal(t, keyID, ctxKeyID)

		ctxModel := r.Context().Value(ContextKeyAPIKeyModel).(*apikeys.APIKey)
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
