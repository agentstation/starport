package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
)

func TestRateLimitMiddlewareEnforcesLimitPerAPIKeyID(t *testing.T) {
	store := storage.NewMockStore()
	rateLimits, err := ratelimit.Open(store, nil)
	require.NoError(t, err)
	server := &Server{
		cfg: &Config{
			EnableRateLimiting:         true,
			RateLimitRequestsPerWindow: 1,
			RateLimitWindow:            time.Minute,
		},
		rateLimits: rateLimits,
	}

	handler := server.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, authenticatedRateLimitRequest("STARPORT_key_one"))

	require.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, "1", first.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", first.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, first.Header().Get("X-RateLimit-Reset"))

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, authenticatedRateLimitRequest("STARPORT_key_one"))

	require.Equal(t, http.StatusTooManyRequests, second.Code)
	assert.Equal(t, "1", second.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", second.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, second.Header().Get("Retry-After"))
	assert.Contains(t, second.Body.String(), "Rate limit exceeded")

	otherKey := httptest.NewRecorder()
	handler.ServeHTTP(otherKey, authenticatedRateLimitRequest("STARPORT_key_two"))

	require.Equal(t, http.StatusOK, otherKey.Code)
	assert.Equal(t, "0", otherKey.Header().Get("X-RateLimit-Remaining"))
}

func TestRateLimitMiddlewareDisabledByDefault(t *testing.T) {
	server := &Server{
		cfg: &Config{},
	}

	called := false
	handler := server.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.True(t, called)
}

func TestRateLimitMiddlewareRequiresAuthenticatedIdentity(t *testing.T) {
	server := &Server{
		cfg: &Config{
			EnableRateLimiting:         true,
			RateLimitRequestsPerWindow: 1,
			RateLimitWindow:            time.Minute,
		},
		rateLimits: mustOpenRateLimitRepository(t),
	}

	handler := server.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Rate limit identity missing")
}

func TestPerKeyRequestLimitOverridesGlobal(t *testing.T) {
	server := &Server{
		cfg: &Config{
			EnableRateLimiting:         true,
			RateLimitRequestsPerWindow: 100,
			RateLimitWindow:            time.Minute,
		},
		rateLimits: mustOpenRateLimitRepository(t),
	}

	handler := server.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	limited := &apikey.APIKey{
		ID:     "key-limited",
		Name:   "limited",
		Scopes: []string{"*"},
		Active: true,
		Limits: &limits.Limits{
			Requests: &limits.RequestLimit{Limit: 1, WindowSeconds: 60},
		},
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, rateLimitRequestWithModel(limited))
	require.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, "1", first.Header().Get("X-RateLimit-Limit"),
		"per-key limit must beat the global window")

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, rateLimitRequestWithModel(limited))
	require.Equal(t, http.StatusTooManyRequests, second.Code)

	// A key without an override still uses the global window.
	unlimited := &apikey.APIKey{ID: "key-global", Name: "global", Scopes: []string{"*"}, Active: true}
	global := httptest.NewRecorder()
	handler.ServeHTTP(global, rateLimitRequestWithModel(unlimited))
	require.Equal(t, http.StatusOK, global.Code)
	assert.Equal(t, "100", global.Header().Get("X-RateLimit-Limit"))
}

func TestPerKeyRequestLimitAppliesWhenGlobalDisabled(t *testing.T) {
	server := &Server{
		cfg:        &Config{},
		rateLimits: mustOpenRateLimitRepository(t),
	}

	handler := server.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	limited := &apikey.APIKey{
		ID:     "key-explicit",
		Name:   "explicit",
		Scopes: []string{"*"},
		Active: true,
		Limits: &limits.Limits{
			Requests: &limits.RequestLimit{Limit: 1, WindowSeconds: 60},
		},
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, rateLimitRequestWithModel(limited))
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, rateLimitRequestWithModel(limited))
	require.Equal(t, http.StatusTooManyRequests, second.Code,
		"an explicit per-key limit is admin intent and applies without the global default")
}

func rateLimitRequestWithModel(apiKey *apikey.APIKey) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := requestctx.WithAPIKeyID(req.Context(), apiKey.ID)
	ctx = requestctx.WithAPIKeyModel(ctx, apiKey)
	return req.WithContext(ctx)
}

func mustOpenRateLimitRepository(t *testing.T) ratelimit.Repository {
	t.Helper()
	repository, err := ratelimit.Open(storage.NewMockStore(), nil)
	require.NoError(t, err)
	return repository
}

func authenticatedRateLimitRequest(keyID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAPIKeyID, keyID)
	return req.WithContext(ctx)
}
