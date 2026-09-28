package limits_test

import (
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/stretchr/testify/require"
)

func TestBudgetHistoryContinuity(t *testing.T) {
	requested := &limits.Limits{Spend: &limits.Budget{Limit: 100, Interval: limits.IntervalDay, HistoryID: "caller-forged"}}
	created := requested.WithBudgetHistory(nil)
	require.NotEmpty(t, created.Spend.HistoryID)
	require.NotEqual(t, "caller-forged", created.Spend.HistoryID)
	require.Equal(t, "caller-forged", requested.Spend.HistoryID)
	requested.Spend.Limit = 200
	updated := requested.WithBudgetHistory(created)
	require.Equal(t, created.Spend.HistoryID, updated.Spend.HistoryID)
	require.EqualValues(t, 200, updated.Spend.Limit)
	requested.Spend.Interval = limits.IntervalMonth
	changed := requested.WithBudgetHistory(updated)
	require.NotEqual(t, updated.Spend.HistoryID, changed.Spend.HistoryID)
	cleared := (&limits.Limits{}).WithBudgetHistory(changed)
	require.Nil(t, cleared.Spend)
	reenabled := requested.WithBudgetHistory(cleared)
	require.NotEqual(t, changed.Spend.HistoryID, reenabled.Spend.HistoryID)
	var absent *limits.Limits
	require.Nil(t, absent.WithBudgetHistory(changed))
	team := (&limits.TeamBudget{Limit: 10, Interval: limits.IntervalDay, HistoryID: "forged"}).WithBudgetHistory(nil)
	require.NotEqual(t, "forged", team.HistoryID)
	rename := (&limits.TeamBudget{Limit: 20, Interval: limits.IntervalDay}).WithBudgetHistory(team)
	require.Equal(t, team.HistoryID, rename.HistoryID)
}
