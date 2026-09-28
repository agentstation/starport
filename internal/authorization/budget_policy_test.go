package authorization

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/stretchr/testify/require"
)

func TestBundleBudgetPolicyUsesOneVerifiedSnapshot(t *testing.T) {
	now := time.Unix(1000, 0)
	id := Identity{Tenant: "account", Subject: "hash"}
	candidate := cacheCandidate(id, now)
	candidate.Key.APIKey.TeamID = "team"
	candidate.Key.APIKey.Limits = &limits.Limits{Tokens: &limits.Budget{Limit: 100, Interval: limits.IntervalDay}}
	candidate.Account.Account.Limits = &limits.Limits{Spend: &limits.Budget{Limit: 200, Interval: limits.IntervalMonth}}
	candidate.Team = &identity.TeamRecord{Revision: 3, Team: identity.Team{ID: "team", Budget: &limits.TeamBudget{Limit: 300, Interval: limits.IntervalWeek}}}
	var loads atomic.Int64
	cache := newTestCache(t, sourceFunc(func(context.Context, Identity) (Candidate, error) {
		loads.Add(1)
		return candidate, nil
	}), cacheTestLimits(), func() (time.Time, bool) { return now, true })
	bundle, err := cache.Resolve(t.Context(), id)
	require.NoError(t, err)
	policy := bundle.BudgetPolicy()
	require.Equal(t, 3, policy.RuleCount())
	candidate.Key.APIKey.Limits.Tokens.Limit = 1
	candidate.Account.Account.Limits.Spend.Limit = 1
	candidate.Team.Team.Budget.Limit = 1
	for index, expected := range []int64{200, 100, 300} {
		rule, found := policy.Rule(index)
		require.True(t, found)
		require.Equal(t, expected, rule.Budget.Limit)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		current, err := cache.Resolve(t.Context(), id)
		if err != nil || current.BudgetPolicy() != policy {
			panic("warm policy changed")
		}
	})
	require.Zero(t, allocations)
	require.EqualValues(t, 1, loads.Load())
}

func TestBundleBudgetPolicyRejectsInvalidLimits(t *testing.T) {
	now := time.Unix(1000, 0)
	id := Identity{Tenant: "account", Subject: "hash"}
	candidate := cacheCandidate(id, now)
	candidate.Key.APIKey.Limits = &limits.Limits{Tokens: &limits.Budget{Limit: -1, Interval: limits.IntervalDay}}
	cache := newTestCache(t, sourceFunc(func(context.Context, Identity) (Candidate, error) { return candidate, nil }), cacheTestLimits(), func() (time.Time, bool) { return now, true })
	_, err := cache.Resolve(t.Context(), id)
	require.ErrorIs(t, err, ErrEvidence)
	require.ErrorIs(t, err, limits.ErrBudgetPolicyUnknown)
}
