package limits

import "errors"

// ErrBudgetPolicyUnknown refuses to infer absent budgets from missing policy.
var ErrBudgetPolicyUnknown = errors.New("budget policy is unknown")

// BudgetHolder contains the consumption policy of one verified holder revision.
// Nil budgets mean confirmed absence only after the caller verifies that record.
type BudgetHolder struct {
	ID       string
	Revision uint64
	Spend    *Budget
	Tokens   *Budget
}

// AdmissionRule is one applicable meter and its admitted policy revision.
type AdmissionRule struct {
	Scope     Scope
	Holder    string
	Revision  uint64
	Dimension Dimension
	Budget    Budget
}

// BudgetPolicy retains immutable account, key, and optional team consumption policy.
// The authorization owner constructs it once when accepting the complete bundle.
type BudgetPolicy struct {
	accountID string
	keyID     string
	teamID    string
	rules     [5]AdmissionRule
	count     int
}

// BudgetPolicyReader checks the original permission before returning its policy.
// Child calls inherit this reader. They cannot invent an unlimited default.
type BudgetPolicyReader interface {
	BudgetPolicy() (*BudgetPolicy, error)
}

// NewBudgetPolicy copies all limits from verified policy records.
// A nil team confirms that the key has no team dependency.
func NewBudgetPolicy(account, key BudgetHolder, team *BudgetHolder) (*BudgetPolicy, error) {
	if account.ID == "" || key.ID == "" || account.Revision == 0 || key.Revision == 0 {
		return nil, ErrBudgetPolicyUnknown
	}
	policy := &BudgetPolicy{accountID: account.ID, keyID: key.ID}
	if err := policy.add(ScopeAccount, account); err != nil {
		return nil, err
	}
	if err := policy.add(ScopeKey, key); err != nil {
		return nil, err
	}
	if team != nil {
		if team.ID == "" || team.Revision == 0 || team.Tokens != nil {
			return nil, ErrBudgetPolicyUnknown
		}
		policy.teamID = team.ID
		if err := policy.add(ScopeTeam, *team); err != nil {
			return nil, err
		}
	}
	return policy, nil
}

func (p *BudgetPolicy) add(scope Scope, holder BudgetHolder) error {
	for _, value := range []struct {
		dimension Dimension
		budget    *Budget
	}{{DimensionSpend, holder.Spend}, {DimensionTokens, holder.Tokens}} {
		if value.budget == nil {
			continue
		}
		if value.budget.Limit <= 0 || !ValidInterval(value.budget.Interval) {
			return ErrBudgetPolicyUnknown
		}
		p.rules[p.count] = AdmissionRule{Scope: scope, Holder: holder.ID, Revision: holder.Revision, Dimension: value.dimension, Budget: *value.budget}
		p.count++
	}
	return nil
}

// Identity returns the original meter populations, including an absent team.
func (p *BudgetPolicy) Identity() (account, key, team string) {
	return p.accountID, p.keyID, p.teamID
}

// RuleCount returns how many required meters apply. Zero confirms no budgets.
func (p *BudgetPolicy) RuleCount() int { return p.count }

// Rule returns a value copy of one meter without copying unrelated account metadata.
func (p *BudgetPolicy) Rule(index int) (AdmissionRule, bool) {
	if index < 0 || index >= p.count {
		return AdmissionRule{}, false
	}
	return p.rules[index], true
}
