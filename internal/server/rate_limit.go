package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/server/requestctx"
)

// headerRateLimitScope names which holder set the limit the other rate-limit
// headers report, so an operator can tell an account cap from a key cap
// without reading the configuration back.
const headerRateLimitScope = "X-RateLimit-Scope"

// scopedRateDecision is one meter's verdict and the holder that set it.
type scopedRateDecision struct {
	scope    limits.Scope
	decision ratelimit.Decision
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rules := s.requestRules(r)
		if len(rules) == 0 || s.rateLimits == nil {
			next.ServeHTTP(w, r)
			return
		}

		keyID, ok := requestctx.GetAPIKeyID(r.Context())
		if !ok || keyID == "" {
			writeProtocolError(w, r, http.StatusInternalServerError, "server_error", "Rate limit identity missing")
			return
		}
		tenantID := requestctx.TenantIDOrDefault(r.Context())

		// Rules arrive account-first and the loop stops at the first refusal,
		// so a request the account cap refuses never draws on the key's
		// allowance. The reverse order does spend an account token on a
		// request the key cap then refuses: the account meter is shared, and
		// holding its token back would need a two-phase reservation across
		// every meter for a case that costs one request of headroom.
		var binding scopedRateDecision
		var bound bool
		for _, rule := range rules {
			subject := rateLimitSubject(rule.Scope, tenantID, keyID)
			window := time.Duration(rule.Limit.WindowSeconds) * time.Second
			decision, err := s.rateLimits.Consume(r.Context(), subject, rule.Limit.Limit, window)
			if err != nil {
				log.Error().Err(err).
					Str("api_key_id", keyID).
					Str("rate_limit_scope", string(rule.Scope)).
					Msg("failed to check rate limit")
				writeProtocolError(w, r, http.StatusInternalServerError, "server_error", "Rate limit error")
				return
			}

			// The tightest meter is the one a client has to pace against, so
			// it owns the reported numbers. A refusal always reports itself.
			if !bound || !decision.Allowed || decision.Remaining < binding.decision.Remaining {
				binding = scopedRateDecision{scope: rule.Scope, decision: decision}
				bound = true
			}
			if !decision.Allowed {
				break
			}
		}

		writeRateLimitHeaders(w, binding.decision.Limit, binding.decision.Remaining, binding.decision.ResetAt)
		w.Header().Set(headerRateLimitScope, string(binding.scope))
		if !binding.decision.Allowed {
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds(binding.decision.ResetAt), 10))
			writeProtocolError(w, r, http.StatusTooManyRequests, "rate_limit_error",
				"Rate limit exceeded: "+string(binding.scope)+" request limit")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requestRules resolves the request-rate meters this request must satisfy.
// Both the account and the key travel in the request context from
// authentication, so neither read reaches storage on the hot path.
func (s *Server) requestRules(r *http.Request) []limits.RequestRule {
	var tenantLimits, keyLimits *limits.Limits
	if record, ok := requestctx.GetTenantRecord(r.Context()); ok && record != nil {
		tenantLimits = record.Limits
	}
	if apiKey, ok := requestctx.GetAPIKeyModel(r.Context()); ok && apiKey != nil {
		keyLimits = apiKey.Limits
	}
	return limits.RequestRules(tenantLimits, keyLimits, s.deploymentRequestLimit())
}

// deploymentRequestLimit expresses the configured global window as a stored
// request limit, reporting nil when no global window applies. A window shorter
// than a second is not expressible as a stored limit, so it rounds up rather
// than truncating to an unlimited zero.
func (s *Server) deploymentRequestLimit() *limits.RequestLimit {
	if !s.rateLimitingEnabled() {
		return nil
	}
	seconds := int64(s.cfg.RateLimitWindow / time.Second)
	if s.cfg.RateLimitWindow%time.Second != 0 {
		seconds++
	}
	return &limits.RequestLimit{Limit: s.cfg.RateLimitRequestsPerWindow, WindowSeconds: seconds}
}

// rateLimitSubject names the counter one meter consumes from. The account
// subject is shared by every key the account holds; that sharing is what makes
// the account cap an account cap.
func rateLimitSubject(scope limits.Scope, tenantID, keyID string) string {
	if scope == limits.ScopeTenant {
		return "tenant:" + tenantID
	}
	return "api_key:" + keyID
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
