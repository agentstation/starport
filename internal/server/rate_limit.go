package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/server/requestctx"
)

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, window := s.effectiveRequestLimit(r)
		if limit <= 0 || window <= 0 || s.rateLimits == nil {
			next.ServeHTTP(w, r)
			return
		}

		keyID, ok := requestctx.GetAPIKeyID(r.Context())
		if !ok || keyID == "" {
			writeProtocolError(w, r, http.StatusInternalServerError, "server_error", "Rate limit identity missing")
			return
		}

		subject := "api_key:" + keyID

		decision, err := s.rateLimits.Consume(r.Context(), subject, limit, window)
		if err != nil {
			log.Error().Err(err).Str("api_key_id", keyID).Msg("failed to check rate limit")
			writeProtocolError(w, r, http.StatusInternalServerError, "server_error", "Rate limit error")
			return
		}

		writeRateLimitHeaders(w, decision.Limit, decision.Remaining, decision.ResetAt)
		if !decision.Allowed {
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds(decision.ResetAt), 10))
			writeProtocolError(w, r, http.StatusTooManyRequests, "rate_limit_error", "Rate limit exceeded")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// effectiveRequestLimit resolves the request limit for one request. A
// per-key request limit is explicit admin intent, so it applies even when
// the global default window is disabled, and it beats the global window
// when both exist.
func (s *Server) effectiveRequestLimit(r *http.Request) (int64, time.Duration) {
	if apiKey, ok := requestctx.GetAPIKeyModel(r.Context()); ok && apiKey != nil &&
		apiKey.Limits != nil && apiKey.Limits.Requests != nil {
		override := apiKey.Limits.Requests
		return override.Limit, time.Duration(override.WindowSeconds) * time.Second
	}
	if !s.rateLimitingEnabled() {
		return 0, 0
	}
	return s.cfg.RateLimitRequestsPerWindow, s.cfg.RateLimitWindow
}

func (s *Server) rateLimitingEnabled() bool {
	return s != nil &&
		s.cfg != nil &&
		s.cfg.EnableRateLimiting &&
		s.cfg.RateLimitRequestsPerWindow > 0 &&
		s.cfg.RateLimitWindow > 0 &&
		s.rateLimits != nil
}

func writeRateLimitHeaders(w http.ResponseWriter, limit, remaining int64, reset time.Time) {
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(limit, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
}

func retryAfterSeconds(reset time.Time) int64 {
	remaining := time.Until(reset)
	if remaining <= 0 {
		return 1
	}

	seconds := int64(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}
