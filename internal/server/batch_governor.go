package server

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/server/requestctx"

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
	teamBudget     func(ctx context.Context, teamID string) (*limits.TeamBudget, error)
}

// batchGovernor builds the line governor over this server's meters.
func (s *Server) batchGovernor() controllers.BatchGovernor {
	return &batchGovernor{
		usage:          s.usage,
		rateLimits:     s.rateLimits,
		deploymentRule: s.deploymentRequestLimit,
		teamBudget:     s.readTeamBudget,
	}
}

// AdmitLine blocks until the line may run, or reports why it never may.
func (g *batchGovernor) AdmitLine(ctx context.Context, admission controllers.BatchAdmission) (context.Context, error) {
	if admission.Reauthorize == nil {
		return nil, unavailableBatchAuthorization()
	}
	current, err := admission.Reauthorize(ctx)
	if err != nil || current == nil {
		return nil, unavailableBatchAuthorization()
	}
	bundle, err := requestctx.Authorization(current)
	if err != nil {
		return nil, unavailableBatchAuthorization()
	}
	key, owner := bundle.Key().APIKey, bundle.Account().Account
	if key.ID != admission.KeyID || owner.ID != admission.AccountID || !key.HasScope("batches:write") {
		return nil, failure.New(failure.Permission, "Batch authorization was withdrawn.", false, failure.ProviderDetails{}, nil)
	}
	admission.TeamID, admission.KeyLimits, admission.AccountLimits = key.TeamID, key.Limits, owner.Limits
	if err := g.admitBudget(current, admission); err != nil {
		return nil, err
	}
	if err := g.admitRate(current, admission); err != nil {
		return nil, err
	}
	if err := inference.CheckPermission(current); err != nil {
		return nil, unavailableBatchAuthorization()
	}
	return current, nil
}

func unavailableBatchAuthorization() error {
	return failure.New(failure.GatewayUnavailable, "Batch authorization is unavailable.", true, failure.ProviderDetails{}, nil)
}

// admitBudget refuses the line when a budget for the current window is
// exhausted. Unknown required policy or usage refuses the line.
func (g *batchGovernor) admitBudget(ctx context.Context, admission controllers.BatchAdmission) error {
	now := time.Now().UTC()
	for _, dimension := range budgetDimensions {
		rules := limits.BudgetRules(admission.AccountLimits, admission.KeyLimits, dimension.name)
		if dimension.name == limits.DimensionSpend && admission.TeamID != "" {
			if g.teamBudget == nil {
				return unavailableBatchBudget()
			}
			budget, err := g.teamBudget(ctx, admission.TeamID)
			if err != nil {
				return unavailableBatchBudget()
			}
			if rule, ok := limits.TeamBudgetRule(budget); ok {
				rules = append(rules, rule)
			}
		}
		for _, rule := range rules {
			scope := budgetScope(rule.Scope, admission.AccountID, admission.KeyID, admission.TeamID)
			if g.usage == nil {
				return unavailableBatchBudget()
			}
			totals, err := g.usage.Totals(ctx, scope, rule.Budget.Interval, now)
			if err != nil {
				return unavailableBatchBudget()
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
			if err := inference.CheckPermission(ctx); err != nil {
				return unavailableBatchAuthorization()
			}
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

func unavailableBatchBudget() error {
	return failure.New(failure.GatewayUnavailable, "Required budget state is unavailable.", true, failure.ProviderDetails{}, nil)
}
