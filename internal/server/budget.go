package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/usage"
)

// Budget response headers. Values are integer nano-USD for spend and
// tokens for the token budget; reset is the Unix time the fixed UTC
// window ends.
//
//nolint:gosec // These are HTTP header names, not credentials.
//
// #nosec G101 -- HTTP header names, not credentials.
const (
	headerBudgetSpendLimit      = "X-Starport-Budget-Spend-Limit"
	headerBudgetSpendRemaining  = "X-Starport-Budget-Spend-Remaining"
	headerBudgetSpendReset      = "X-Starport-Budget-Spend-Reset"
	headerBudgetTokensLimit     = "X-Starport-Budget-Tokens-Limit"
	headerBudgetTokensRemaining = "X-Starport-Budget-Tokens-Remaining"
	headerBudgetTokensReset     = "X-Starport-Budget-Tokens-Reset"
)

// enforceBudgets rejects a request with 402 when the key's configured
// spend or token budget for its fixed UTC window is exhausted. A budget
// read failure allows the request and logs loudly: a broken meter must
// not take the gateway down (D6).
func (s *Server) enforceBudgets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey, ok := requestctx.GetAPIKeyModel(r.Context())
		if !ok || apiKey == nil || apiKey.Limits == nil || s.usage == nil {
			next.ServeHTTP(w, r)
			return
		}
		limits := apiKey.Limits
		if limits.Spend == nil && limits.Tokens == nil {
			next.ServeHTTP(w, r)
			return
		}

		now := time.Now().UTC()
		if !s.allowBudget(w, r, apiKey, limits.Spend, "spend", now,
			func(t usage.Totals) int64 { return t.SpendNanoUSD },
			headerBudgetSpendLimit, headerBudgetSpendRemaining, headerBudgetSpendReset) {
			return
		}
		if !s.allowBudget(w, r, apiKey, limits.Tokens, "token", now,
			func(t usage.Totals) int64 { return t.Tokens },
			headerBudgetTokensLimit, headerBudgetTokensRemaining, headerBudgetTokensReset) {
			return
		}

		next.ServeHTTP(w, r)
	})
}

// allowBudget checks one budget dimension. It reports true when the
// request may proceed and writes the 402 response itself otherwise.
func (s *Server) allowBudget(
	w http.ResponseWriter,
	r *http.Request,
	apiKey *identity.APIKey,
	budget *identity.Budget,
	dimension string,
	now time.Time,
	used func(usage.Totals) int64,
	limitHeader, remainingHeader, resetHeader string,
) bool {
	if budget == nil {
		return true
	}

	totals, err := s.usage.Totals(r.Context(), apiKey.ID, budget.Interval, now)
	if err != nil {
		// Fail open: a budget read failure must not reject traffic.
		log.Error().Err(err).
			Str("api_key_id", apiKey.ID).
			Str("budget", dimension).
			Str("interval", budget.Interval).
			Msg("budget read failed; allowing request")
		return true
	}

	consumed := used(totals)
	remaining := budget.Limit - consumed
	if remaining < 0 {
		remaining = 0
	}
	_, windowEnd := usage.Window(budget.Interval, now)

	w.Header().Set(limitHeader, strconv.FormatInt(budget.Limit, 10))
	w.Header().Set(remainingHeader, strconv.FormatInt(remaining, 10))
	w.Header().Set(resetHeader, strconv.FormatInt(windowEnd.Unix(), 10))

	if consumed >= budget.Limit {
		writeProtocolError(w, r, http.StatusPaymentRequired, "permission_error",
			"Insufficient quota: "+dimension+" budget exhausted for the current "+budget.Interval+" window")
		return false
	}
	return true
}
