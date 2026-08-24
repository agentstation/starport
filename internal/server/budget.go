package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/usage"
)

// Budget response headers. Values are integer nano-USD for spend and
// tokens for the token budget; reset is the Unix time the fixed UTC
// window ends. The scope header names which holder set the reported budget,
// so an operator can tell an account cap from a key cap.
//
// #nosec G101 -- HTTP header names, not credentials.
//
//nolint:gosec // These are HTTP header names, not credentials.
const (
	headerBudgetSpendLimit      = "X-Starport-Budget-Spend-Limit"
	headerBudgetSpendRemaining  = "X-Starport-Budget-Spend-Remaining"
	headerBudgetSpendReset      = "X-Starport-Budget-Spend-Reset"
	headerBudgetSpendScope      = "X-Starport-Budget-Spend-Scope"
	headerBudgetTokensLimit     = "X-Starport-Budget-Tokens-Limit"
	headerBudgetTokensRemaining = "X-Starport-Budget-Tokens-Remaining"
	headerBudgetTokensReset     = "X-Starport-Budget-Tokens-Reset"
	headerBudgetTokensScope     = "X-Starport-Budget-Tokens-Scope"
)

// budgetDimension binds one consumption dimension to the counter that meters
// it and the headers that report it.
type budgetDimension struct {
	name                         limits.Dimension
	used                         func(usage.Totals) int64
	limitHeader, remainingHeader string
	resetHeader, scopeHeader     string
}

var budgetDimensions = []budgetDimension{
	{
		name:            limits.DimensionSpend,
		used:            func(t usage.Totals) int64 { return t.SpendNanoUSD },
		limitHeader:     headerBudgetSpendLimit,
		remainingHeader: headerBudgetSpendRemaining,
		resetHeader:     headerBudgetSpendReset,
		scopeHeader:     headerBudgetSpendScope,
	},
	{
		name:            limits.DimensionTokens,
		used:            func(t usage.Totals) int64 { return t.Tokens },
		limitHeader:     headerBudgetTokensLimit,
		remainingHeader: headerBudgetTokensRemaining,
		resetHeader:     headerBudgetTokensReset,
		scopeHeader:     headerBudgetTokensScope,
	},
}

// enforceBudgets rejects a request with 402 when a budget for its fixed UTC
// window is exhausted. Both the account budget and the key budget apply: the
// account counter sums every key the account holds, so neither total answers
// for the other. A budget read failure allows the request and logs loudly: a
// broken meter must not take the gateway down (D6).
func (s *Server) enforceBudgets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey, ok := requestctx.GetAPIKeyModel(r.Context())
		if !ok || apiKey == nil || s.usage == nil {
			next.ServeHTTP(w, r)
			return
		}

		var tenantLimits *limits.Limits
		if record, ok := requestctx.GetTenantRecord(r.Context()); ok && record != nil {
			tenantLimits = record.Limits
		}
		tenantID := requestctx.TenantIDOrDefault(r.Context())

		now := time.Now().UTC()
		for _, dimension := range budgetDimensions {
			rules := limits.BudgetRules(tenantLimits, apiKey.Limits, dimension.name)
			if !s.allowBudget(w, r, dimension, rules, tenantID, apiKey.ID, now) {
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// scopedBudget is one budget meter's reading and the holder that set it.
type scopedBudget struct {
	scope     limits.Scope
	limit     int64
	remaining int64
	interval  string
	windowEnd time.Time
	exhausted bool
}

// allowBudget checks every rule for one budget dimension. It reports true when
// the request may proceed and writes the 402 response itself otherwise.
func (s *Server) allowBudget(
	w http.ResponseWriter,
	r *http.Request,
	dimension budgetDimension,
	rules []limits.BudgetRule,
	tenantID, keyID string,
	now time.Time,
) bool {
	var binding scopedBudget
	var bound bool

	for _, rule := range rules {
		scope := budgetScope(rule.Scope, tenantID, keyID)
		totals, err := s.usage.Totals(r.Context(), scope, rule.Budget.Interval, now)
		if err != nil {
			// Fail open: a budget read failure must not reject traffic.
			log.Error().Err(err).
				Str("usage_scope", scope.String()).
				Str("budget", string(dimension.name)).
				Str("interval", rule.Budget.Interval).
				Msg("budget read failed; allowing request")
			continue
		}

		consumed := dimension.used(totals)
		remaining := max(rule.Budget.Limit-consumed, 0)
		_, windowEnd := usage.Window(rule.Budget.Interval, now)
		reading := scopedBudget{
			scope:     rule.Scope,
			limit:     rule.Budget.Limit,
			remaining: remaining,
			interval:  rule.Budget.Interval,
			windowEnd: windowEnd,
			exhausted: consumed >= rule.Budget.Limit,
		}

		// The tightest meter owns the reported numbers, and an exhausted
		// meter always reports itself: it is the one refusing the request.
		if !bound || reading.exhausted || (!binding.exhausted && reading.remaining < binding.remaining) {
			binding = reading
			bound = true
		}
		if reading.exhausted {
			break
		}
	}

	if !bound {
		return true
	}

	w.Header().Set(dimension.limitHeader, strconv.FormatInt(binding.limit, 10))
	w.Header().Set(dimension.remainingHeader, strconv.FormatInt(binding.remaining, 10))
	w.Header().Set(dimension.resetHeader, strconv.FormatInt(binding.windowEnd.Unix(), 10))
	w.Header().Set(dimension.scopeHeader, string(binding.scope))

	if binding.exhausted {
		writeProtocolError(w, r, http.StatusPaymentRequired, "permission_error",
			"Insufficient quota: "+string(binding.scope)+" "+string(dimension.name)+
				" budget exhausted for the current "+binding.interval+" window")
		return false
	}
	return true
}

// budgetScope names the counter set one budget meter reads.
func budgetScope(scope limits.Scope, tenantID, keyID string) usage.Scope {
	if scope == limits.ScopeTenant {
		return usage.TenantScope(tenantID)
	}
	return usage.KeyScope(keyID)
}
