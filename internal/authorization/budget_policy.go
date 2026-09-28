package authorization

import "github.com/agentstation/starport/internal/limits"

func budgetPolicy(candidate Candidate) (*limits.BudgetPolicy, error) {
	holder := func(id string, revision uint64, policy *limits.Limits) limits.BudgetHolder {
		value := limits.BudgetHolder{ID: id, Revision: revision}
		if policy != nil {
			value.Spend, value.Tokens = policy.Spend, policy.Tokens
		}
		return value
	}
	var team *limits.BudgetHolder
	if candidate.Team != nil {
		team = &limits.BudgetHolder{ID: candidate.Team.Team.ID, Revision: candidate.Team.Revision}
		if budget := candidate.Team.Team.Budget; budget != nil {
			team.Spend = &limits.Budget{Limit: budget.Limit, Interval: budget.Interval, HistoryID: budget.HistoryID}
		}
	}
	return limits.NewBudgetPolicy(
		holder(candidate.Account.Account.ID, candidate.Account.Revision, candidate.Account.Account.Limits),
		holder(candidate.Key.APIKey.ID, candidate.Key.Revision, candidate.Key.APIKey.Limits), team,
	)
}

// BudgetPolicy returns immutable consumption policy from this accepted bundle.
// The request's permission reader must check the bundle receipt before use.
func (b *Bundle) BudgetPolicy() *limits.BudgetPolicy { return b.budgets }
