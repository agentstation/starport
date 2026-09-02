package controllers

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/usage"
)

// Budget entry field names. A budget reads the same on every holder that
// sets one — a key, an account, or a team — so the console draws one meter
// for all three.
const (
	fieldBudgetUsed        = "used"
	fieldBudgetRemaining   = "remaining"
	fieldBudgetWindowStart = "window_start"
	fieldBudgetWindowEnd   = "window_end"
)

// spendOf and tokensOf pick the counter a budget dimension meters.
func spendOf(t usage.Totals) int64  { return t.SpendNanoUSD }
func tokensOf(t usage.Totals) int64 { return t.Tokens }

// budgetEntry shapes one budget's reading for the fixed UTC window that
// contains now: the ceiling, the interval, what the window consumed, what is
// left, and the window's two edges, so a reader knows when the meter resets
// without knowing the interval rule.
func budgetEntry(budget limits.Budget, consumed int64, now time.Time) map[string]any {
	entry := budgetWindow(budget, now)
	entry[fieldBudgetUsed] = consumed
	entry[fieldBudgetRemaining] = max(budget.Limit-consumed, 0)
	return entry
}

// budgetUnavailable shapes a budget whose meter could not be read. The
// ceiling and the window still travel: the operator set them, and only the
// consumption is missing.
func budgetUnavailable(budget limits.Budget, now time.Time) map[string]any {
	entry := budgetWindow(budget, now)
	entry[fieldError] = systemInfoUnavailable
	return entry
}

func budgetWindow(budget limits.Budget, now time.Time) map[string]any {
	start, end := usage.Window(budget.Interval, now)
	return map[string]any{
		fieldLimit:             budget.Limit,
		fieldInterval:          budget.Interval,
		fieldBudgetWindowStart: start.Format(time.RFC3339),
		fieldBudgetWindowEnd:   end.Format(time.RFC3339),
	}
}

// budgetMeter reads one budget's consumption from the usage counters. A
// deployment with no usage store, or a failed read, answers the unavailable
// shape rather than a zero that would read as "nothing spent".
func budgetMeter(
	ctx context.Context,
	records usage.Repository,
	scope usage.Scope,
	budget limits.Budget,
	used func(usage.Totals) int64,
	now time.Time,
) map[string]any {
	if records == nil {
		return budgetUnavailable(budget, now)
	}
	totals, err := records.Totals(ctx, scope, budget.Interval, now)
	if err != nil {
		log.Error().Err(err).Str("usage_scope", scope.String()).Str(fieldInterval, budget.Interval).
			Msg("Failed to read budget usage totals")
		return budgetUnavailable(budget, now)
	}
	return budgetEntry(budget, used(totals), now)
}

// limitBudgets meters the spend and token budgets a Limits carrier sets. It
// answers nil when the carrier sets neither, so the field stays absent on a
// holder with no budget rather than reading as an empty meter.
func limitBudgets(
	ctx context.Context,
	records usage.Repository,
	scope usage.Scope,
	carrier *limits.Limits,
	now time.Time,
) map[string]any {
	if carrier == nil {
		return nil
	}
	budgets := map[string]any{}
	if budget := carrier.Spend; budget != nil {
		budgets[fieldSpend] = budgetMeter(ctx, records, scope, *budget, spendOf, now)
	}
	if budget := carrier.Tokens; budget != nil {
		budgets[fieldTokens] = budgetMeter(ctx, records, scope, *budget, tokensOf, now)
	}
	if len(budgets) == 0 {
		return nil
	}
	return budgets
}
