package limits

// A team budget meters a third population beside the account and the key: it
// counts every key the team's grant resolved, across every account the team
// reaches. It therefore joins the rule list the way the account and key rules
// join each other — every meter runs — rather than resolving to a tightest
// value, for the reason the package comment in rules.go gives.

// ScopeTeam is a team-wide limit. It meters every key attributed to the team.
const ScopeTeam Scope = "team"

// TeamBudget bounds a team's consumption of one dimension inside one fixed
// UTC interval. It is a distinct named type rather than a bare Budget so the
// identity seam can carry it without importing the meaning of every other
// limit dimension a Limits carrier holds.
type TeamBudget struct {
	Limit    int64  `json:"limit"`
	Interval string `json:"interval"`
}

// Validate checks the team budget invariants.
func (b *TeamBudget) Validate() error {
	if b == nil {
		return nil
	}
	if b.Limit <= 0 {
		return ErrInvalidBudgetLimit
	}
	if !ValidInterval(b.Interval) {
		return ErrInvalidBudgetInterval
	}
	return nil
}

// Clone returns a deep copy of the team budget.
func (b *TeamBudget) Clone() *TeamBudget {
	if b == nil {
		return nil
	}
	clone := *b
	return &clone
}

// TeamBudgetRule projects a stored team budget into the consumption rule the
// enforcement path runs beside the account and key rules. It reports false
// when the team sets no budget.
func TeamBudgetRule(budget *TeamBudget) (BudgetRule, bool) {
	if budget == nil {
		return BudgetRule{}, false
	}
	return BudgetRule{Scope: ScopeTeam, Budget: Budget(*budget)}, true
}
