package limits

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequestRulesKeepBothMetersAndOrderAccountFirst states the two properties
// enforcement depends on. Both meters survive, because collapsing them to the
// smaller value would let an account with N keys spend N times its own cap. And
// the account rule comes first, because a caller stops at the first refusal:
// the reverse order spends an account token on a request the key cap then
// refuses, and that token is never returned.
func TestRequestRulesKeepBothMetersAndOrderAccountFirst(t *testing.T) {
	rules := RequestRules(
		&Limits{Requests: &RequestLimit{Limit: 1, WindowSeconds: 60}},
		&Limits{Requests: &RequestLimit{Limit: 100, WindowSeconds: 60}},
		nil,
	)

	require.Len(t, rules, 2, "an account limit and a key limit are two meters, not two candidates")
	assert.Equal(t, ScopeTenant, rules[0].Scope)
	assert.Equal(t, int64(1), rules[0].Limit.Limit)
	assert.Equal(t, ScopeKey, rules[1].Scope)
	assert.Equal(t, int64(100), rules[1].Limit.Limit)
}

// TestTheDeploymentWindowOnlyFillsInForAKeyThatSetsNone covers the fallback.
// An explicit key limit is admin intent about that key, so the gateway's global
// window must not override it in either direction.
func TestTheDeploymentWindowOnlyFillsInForAKeyThatSetsNone(t *testing.T) {
	deployment := &RequestLimit{Limit: 60, WindowSeconds: 60}

	filled := RequestRules(nil, nil, deployment)
	require.Len(t, filled, 1)
	assert.Equal(t, ScopeKey, filled[0].Scope)
	assert.Equal(t, int64(60), filled[0].Limit.Limit)

	explicit := RequestRules(nil, &Limits{Requests: &RequestLimit{Limit: 5, WindowSeconds: 1}}, deployment)
	require.Len(t, explicit, 1)
	assert.Equal(t, int64(5), explicit[0].Limit.Limit, "an explicit key limit wins over the deployment window")

	assert.Empty(t, RequestRules(nil, nil, nil), "no window anywhere means no rate meter")
}

// TestBudgetRulesSelectOneDimensionPerHolder proves a dimension one holder left
// unset adds no meter, so an account capping only spend does not silently cap
// tokens as well.
func TestBudgetRulesSelectOneDimensionPerHolder(t *testing.T) {
	account := &Limits{Spend: &Budget{Limit: 1_000, Interval: IntervalDay}}
	key := &Limits{
		Spend:  &Budget{Limit: 100, Interval: IntervalDay},
		Tokens: &Budget{Limit: 50, Interval: IntervalMonth},
	}

	spend := BudgetRules(account, key, DimensionSpend)
	require.Len(t, spend, 2)
	assert.Equal(t, ScopeTenant, spend[0].Scope)
	assert.Equal(t, int64(1_000), spend[0].Budget.Limit)
	assert.Equal(t, ScopeKey, spend[1].Scope)

	tokens := BudgetRules(account, key, DimensionTokens)
	require.Len(t, tokens, 1, "the account set no token budget, so only the key meters tokens")
	assert.Equal(t, ScopeKey, tokens[0].Scope)

	assert.Empty(t, BudgetRules(account, key, Dimension("storage")),
		"an unknown dimension bounds nothing rather than falling back to a budget it was not asked for")
	assert.Empty(t, BudgetRules(nil, nil, DimensionSpend))
}
