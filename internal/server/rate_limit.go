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
		if !s.rateLimitingEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		keyID, ok := requestctx.GetAPIKeyID(r.Context())
		if !ok || keyID == "" {
			writeProtocolError(w, r, http.StatusInternalServerError, "server_error", "Rate limit identity missing")
			return
		}

		limit := s.cfg.RateLimitRequestsPerWindow
		window := s.cfg.RateLimitWindow
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
