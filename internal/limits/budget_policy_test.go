package limits_test

import (
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/stretchr/testify/require"
)

func TestBudgetPolicyKeepsEveryHolderAndRevision(t *testing.T) {
	spend := &limits.Budget{Limit: 1000, Interval: limits.IntervalMonth}
	tokens := &limits.Budget{Limit: 2000, Interval: limits.IntervalDay}
	policy, err := limits.NewBudgetPolicy(
		limits.BudgetHolder{ID: "account", Revision: 4, Spend: spend, Tokens: tokens},
		limits.BudgetHolder{ID: "key", Revision: 7, Spend: spend, Tokens: tokens},
		&limits.BudgetHolder{ID: "team", Revision: 9, Spend: spend},
	)
	require.NoError(t, err)
	require.Equal(t, 5, policy.RuleCount())
	account, key, team := policy.Identity()
	require.Equal(t, "account", account)
	require.Equal(t, "key", key)
	require.Equal(t, "team", team)
	spend.Limit, tokens.Limit = 1, 1
	want := []limits.AdmissionRule{
		{Scope: limits.ScopeAccount, Holder: "account", Revision: 4, Dimension: limits.DimensionSpend, Budget: limits.Budget{Limit: 1000, Interval: limits.IntervalMonth}},
		{Scope: limits.ScopeAccount, Holder: "account", Revision: 4, Dimension: limits.DimensionTokens, Budget: limits.Budget{Limit: 2000, Interval: limits.IntervalDay}},
		{Scope: limits.ScopeKey, Holder: "key", Revision: 7, Dimension: limits.DimensionSpend, Budget: limits.Budget{Limit: 1000, Interval: limits.IntervalMonth}},
		{Scope: limits.ScopeKey, Holder: "key", Revision: 7, Dimension: limits.DimensionTokens, Budget: limits.Budget{Limit: 2000, Interval: limits.IntervalDay}},
		{Scope: limits.ScopeTeam, Holder: "team", Revision: 9, Dimension: limits.DimensionSpend, Budget: limits.Budget{Limit: 1000, Interval: limits.IntervalMonth}},
	}
	for index, expected := range want {
		rule, found := policy.Rule(index)
		require.True(t, found)
		require.Equal(t, expected, rule)
		rule.Budget.Limit = 0
		reread, _ := policy.Rule(index)
		require.Equal(t, expected, reread)
	}
	for _, index := range []int{-1, 5} {
		_, found := policy.Rule(index)
		require.False(t, found)
	}
	var sum int64
	allocations := testing.AllocsPerRun(1000, func() {
		for index := range policy.RuleCount() {
			rule, _ := policy.Rule(index)
			sum += rule.Budget.Limit
		}
	})
	require.Positive(t, sum)
	require.Zero(t, allocations)
}

func TestBudgetPolicyDistinguishesAbsenceFromUnknown(t *testing.T) {
	account, key := limits.BudgetHolder{ID: "account", Revision: 1}, limits.BudgetHolder{ID: "key", Revision: 1}
	policy, err := limits.NewBudgetPolicy(account, key, nil)
	require.NoError(t, err)
	require.Zero(t, policy.RuleCount())
	_, _, team := policy.Identity()
	require.Empty(t, team)
	for _, value := range []limits.BudgetHolder{
		{}, {ID: "key"}, {Revision: 1},
		{ID: "key", Revision: 1, Spend: &limits.Budget{Limit: -1, Interval: limits.IntervalDay}},
		{ID: "key", Revision: 1, Tokens: &limits.Budget{Limit: 10, Interval: "unsupported"}},
	} {
		_, err := limits.NewBudgetPolicy(account, value, nil)
		require.ErrorIs(t, err, limits.ErrBudgetPolicyUnknown)
	}
	_, err = limits.NewBudgetPolicy(account, key, &limits.BudgetHolder{})
	require.ErrorIs(t, err, limits.ErrBudgetPolicyUnknown)
}
