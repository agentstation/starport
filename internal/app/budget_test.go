package app

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/server"
	"github.com/stretchr/testify/require"
)

func TestBudgetPolicyPreparationUsesProductionComposition(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Identity.OAuth.GitHub.ClientID = "fixture-client"
	cfg.Identity.OAuth.GitHub.ClientSecret = "fixture-secret"
	factories := explicitTestFactories(t)
	var dependencies server.Dependencies
	factories.newServer = func(_ *server.Config, value server.Dependencies) (httpRuntime, error) {
		dependencies = value
		return newBlockingHTTPRuntime(), nil
	}
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	require.NotNil(t, application.budget.admission)
	team, err := dependencies.Identity.Teams.Create(t.Context(), identity.Team{ID: "budget-team", Name: "Budget team", Budget: &limits.TeamBudget{Limit: 1000, Interval: limits.IntervalDay}})
	require.NoError(t, err)
	key := apikey.APIKey{ID: "team-key", Name: "team-key", Hash: "team-hash", Active: true, TeamID: team.Team.ID, Scopes: []string{"*"}, CreatedAt: time.Now()}
	_, err = dependencies.APIKeys.Create(t.Context(), key)
	require.NoError(t, err)
	bundle, err := dependencies.Authorization.Resolve(t.Context(), authorization.Identity{Subject: key.Hash})
	require.NoError(t, err, "one bounded cold load must prepare history and reread its changed SQL revision")
	require.Equal(t, 1, bundle.BudgetPolicy().RuleCount())
	rule, ok := bundle.BudgetPolicy().Rule(0)
	require.True(t, ok)
	require.Equal(t, team.Team.Budget.HistoryID, rule.Budget.HistoryID)
	before, err := application.authorization.sqlRevision.Read(t.Context())
	require.NoError(t, err)
	// A warm lookup must return the same accepted bundle without another claim.
	warm, err := dependencies.Authorization.Resolve(t.Context(), authorization.Identity{Subject: key.Hash})
	require.NoError(t, err)
	require.Same(t, bundle, warm)
	claimed, err := dependencies.Identity.Teams.(reservation.TeamHistoryAuthority).ClaimBudgetHistory(t.Context(), team.Team.ID, team.Team.Budget.Interval, team.Team.Budget.HistoryID)
	require.NoError(t, err)
	require.False(t, claimed)
	after, err := application.authorization.sqlRevision.Read(t.Context())
	require.NoError(t, err)
	require.Equal(t, before, after, "a consumed grant must not revoke other callers")
	require.NoError(t, warm.Permit().Check(time.Now(), true))

	key.ID, key.Hash = "second-team-key", "second-team-hash"
	_, err = dependencies.APIKeys.Create(t.Context(), key)
	require.NoError(t, err)
	unrelated, err := dependencies.Authorization.Resolve(t.Context(), authorization.Identity{Subject: testAPIKey().Hash})
	require.NoError(t, err)
	for _, prefix := range []string{"budget:v1:team-origin:", "budget:v1:history:"} {
		keys, err := application.store.ScanWithPrefix(t.Context(), prefix, 100)
		require.NoError(t, err)
		require.Len(t, keys, 1)
		require.NoError(t, application.store.BatchDelete(t.Context(), keys))
	}
	_, err = dependencies.Authorization.Resolve(t.Context(), authorization.Identity{Subject: key.Hash})
	require.ErrorIs(t, err, reservation.ErrHistoryUnknown, "missing history cannot recreate the consumed SQL grant")
	after, err = application.authorization.sqlRevision.Read(t.Context())
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, unrelated.Permit().Check(time.Now(), true), "unknown team history cannot revoke unrelated callers")
}
