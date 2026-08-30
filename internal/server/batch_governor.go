package server

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/usage"
)

// batchGovernor admits batch lines under the same meters the middleware runs
// on an online request. It lives here rather than in the controllers package
// because the meters are middleware-owned state: the usage totals behind the
// budget check and the rate-limit repository behind the request pacing.
//
// The two checks run in the online order, budget before rate, so a line a
// budget refuses never draws a rate token. The behaviors differ on refusal:
// a budget refusal fails the line, because no amount of waiting refills a
// window this batch is itself draining, while a rate refusal waits for the
// window to reset, because pacing background work is what the limit is for.
type batchGovernor struct {
	usage          usage.Repository
	rateLimits     ratelimit.Repository
	deploymentRule func() *limits.RequestLimit
}

// batchGovernor builds the line governor over this server's meters.
func (s *Server) batchGovernor() controllers.BatchGovernor {
	return &batchGovernor{
		usage:          s.usage,
		rateLimits:     s.rateLimits,
		deploymentRule: s.deploymentRequestLimit,
	}
}

// AdmitLine blocks until the line may run, or reports why it never may.
func (g *batchGovernor) AdmitLine(ctx context.Context, admission controllers.BatchAdmission) error {
	if err := g.admitBudget(ctx, admission); err != nil {
		return err
	}
	return g.admitRate(ctx, admission)
}

// admitBudget refuses the line when a budget for the current window is
// exhausted, in the same words the online 402 uses. A read failure allows
// the line and logs loudly, exactly as the middleware fails open (D6).
func (g *batchGovernor) admitBudget(ctx context.Context, admission controllers.BatchAdmission) error {
	if g.usage == nil {
		return nil
	}
	now := time.Now().UTC()
	for _, dimension := range budgetDimensions {
		rules := limits.BudgetRules(admission.AccountLimits, admission.KeyLimits, dimension.name)
		for _, rule := range rules {
			scope := budgetScope(rule.Scope, admission.AccountID, admission.KeyID)
			totals, err := g.usage.Totals(ctx, scope, rule.Budget.Interval, now)
			if err != nil {
				log.Error().Err(err).
					Str("usage_scope", scope.String()).
					Str("budget", string(dimension.name)).
					Str("interval", rule.Budget.Interval).
					Msg("batch budget read failed; allowing line")
				continue
			}
			if dimension.used(totals) >= rule.Budget.Limit {
				return &controllers.BatchBudgetError{
					Message: "Insufficient quota: " + string(rule.Scope) + " " +
						string(dimension.name) + " budget exhausted for the current " +
						rule.Budget.Interval + " window",
				}
			}
		}
	}
	return nil
}

// admitRate draws one request token from every meter an online request
// draws from, waiting out each refusal until the meter resets. The wait is
// the point: a batch line has no caller to answer 429 to, so the governor
// paces the batch against the same windows instead.
func (g *batchGovernor) admitRate(ctx context.Context, admission controllers.BatchAdmission) error {
	if g.rateLimits == nil {
		return nil
	}
	var deploymentRule *limits.RequestLimit
	if g.deploymentRule != nil {
		deploymentRule = g.deploymentRule()
	}
	rules := limits.RequestRules(admission.AccountLimits, admission.KeyLimits, deploymentRule)
	for _, rule := range rules {
		subject := rateLimitSubject(rule.Scope, admission.AccountID, admission.KeyID)
		window := time.Duration(rule.Limit.WindowSeconds) * time.Second
		for {
			decision, err := g.rateLimits.Consume(ctx, subject, rule.Limit.Limit, window)
			if err != nil {
				return err
			}
			if decision.Allowed {
				break
			}
			if err := sleepUntil(ctx, decision.ResetAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// sleepUntil waits for the reset moment, with a floor so a reset already in
// the past cannot turn the retry loop into a hot spin.
func sleepUntil(ctx context.Context, reset time.Time) error {
	wait := max(time.Until(reset), time.Second)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
