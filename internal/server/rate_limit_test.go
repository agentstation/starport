package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/ratelimit"
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
