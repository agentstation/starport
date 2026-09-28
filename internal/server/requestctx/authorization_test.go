package requestctx

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits"
	"github.com/stretchr/testify/require"
)

type budgetSource struct{ candidate authorization.Candidate }

func (s budgetSource) Load(context.Context, authorization.Identity) (authorization.Candidate, error) {
	return s.candidate, nil
}

func TestChildBudgetPolicyKeepsOriginalPermission(t *testing.T) {
	now := time.Now()
	var elapsed time.Duration
	fence := authorization.NewFence("policy", "epoch")
	authorities, err := authorization.NewAuthoritySet(fence)
	require.NoError(t, err)
	source := budgetSource{candidate: authorization.Candidate{
		Key: apikey.Record{Revision: 1, APIKey: apikey.APIKey{
			ID: "key", Hash: "hash", AccountID: "account", Active: true,
			Limits: &limits.Limits{Tokens: &limits.Budget{Limit: 100, Interval: limits.IntervalDay}},
		}},
		Account:  account.Record{Revision: 1, Account: account.Account{ID: "account", Active: true}},
		Evidence: []authorization.Evidence{{Authority: "policy", Epoch: "epoch", Sequence: 1, VerifiedAt: now, ValidUntil: now.Add(time.Minute)}},
	}}
	clock := func() (time.Time, bool) { return now, true }
	cache, err := authorization.NewCache(source, authorities, authorization.CacheLimits{
		Entries: 1, Bytes: 8192, BundleBytes: 8192, ConcurrentLoads: 1, TenantLoads: 1,
		LoadTimeout: time.Second, PermissionLifetime: time.Minute,
	}, clock, func() (time.Duration, bool) { return elapsed, true })
	require.NoError(t, err)
	t.Cleanup(cache.Close)
	bundle, err := cache.Resolve(t.Context(), authorization.Identity{Tenant: "account", Subject: "hash"})
	require.NoError(t, err)
	ctx := WithAuthorization(t.Context(), bundle, clock, nil)
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, ok := inference.RequestPermission(child).(limits.BudgetPolicyReader)
	require.True(t, ok)
	policy, err := reader.BudgetPolicy()
	require.NoError(t, err)
	require.Equal(t, 1, policy.RuleCount())
	allocations := testing.AllocsPerRun(1000, func() {
		current, err := reader.BudgetPolicy()
		if err != nil || current != policy {
			panic("child policy changed")
		}
	})
	require.Zero(t, allocations)
	if err := fence.Require(2); err != nil {
		t.Fatal(err)
	}
	_, err = reader.BudgetPolicy()
	require.ErrorIs(t, err, authorization.ErrWithdrawn)
}

func TestMissingBudgetPermissionCannotMeanUnlimited(t *testing.T) {
	state := &authorizationState{}
	policy, err := state.BudgetPolicy()
	require.Nil(t, policy)
	require.ErrorIs(t, err, authorization.ErrUnavailable)
}
