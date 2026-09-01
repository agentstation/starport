package limits

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTeamBudgetValidate proves a stored team budget refuses the same shapes a
// holder budget refuses, so a corrupt or hand-edited team record cannot arm a
// meter with a window the usage seam does not maintain.
func TestTeamBudgetValidate(t *testing.T) {
	var unset *TeamBudget
	require.NoError(t, unset.Validate(), "no budget is a valid team")

	require.NoError(t, (&TeamBudget{Limit: 1_000, Interval: IntervalMonth}).Validate())

	assert.ErrorIs(t, (&TeamBudget{Limit: 0, Interval: IntervalDay}).Validate(), ErrInvalidBudgetLimit)
	assert.ErrorIs(t, (&TeamBudget{Limit: -5, Interval: IntervalDay}).Validate(), ErrInvalidBudgetLimit)
	assert.ErrorIs(t, (&TeamBudget{Limit: 10, Interval: "quarter"}).Validate(), ErrInvalidBudgetInterval)
}

// TestTeamBudgetRule proves the projection into the enforcement vocabulary:
// a set budget becomes one team-scoped rule, and an unset one adds no meter.
func TestTeamBudgetRule(t *testing.T) {
	rule, ok := TeamBudgetRule(&TeamBudget{Limit: 2_000, Interval: IntervalWeek})
	require.True(t, ok)
	assert.Equal(t, ScopeTeam, rule.Scope)
	assert.Equal(t, Budget{Limit: 2_000, Interval: IntervalWeek}, rule.Budget)

	_, ok = TeamBudgetRule(nil)
	assert.False(t, ok, "a team with no budget meters nothing")
}

// TestTeamBudgetClone proves the copy shares no storage with the source.
func TestTeamBudgetClone(t *testing.T) {
	var unset *TeamBudget
	assert.Nil(t, unset.Clone())

	source := &TeamBudget{Limit: 500, Interval: IntervalDay}
	clone := source.Clone()
	require.NotNil(t, clone)
	clone.Limit = 9
	assert.Equal(t, int64(500), source.Limit)
}
