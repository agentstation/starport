package revision_test

import (
	"testing"

	"github.com/agentstation/starport/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountSelectionUsesCurrentDirectAndTeamGrants(t *testing.T) {
	db := revisionDatabase(t)
	repos, err := identity.Open(db)
	require.NoError(t, err)
	_, err = repos.Users.Create(t.Context(), identity.User{ID: "person", Subject: "test:person"})
	require.NoError(t, err)
	_, err = repos.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team"})
	require.NoError(t, err)
	_, err = repos.Memberships.Add(t.Context(), identity.Membership{UserID: "person", TeamID: "team"})
	require.NoError(t, err)
	_, err = repos.AccountGrants.ResolveAccount(t.Context(), "person", "")
	require.ErrorIs(t, err, identity.ErrAccountGrantNotFound)
	_, err = repos.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: "first", TeamID: "team"})
	require.NoError(t, err)
	selected, err := repos.AccountGrants.ResolveAccount(t.Context(), "person", "")
	require.NoError(t, err)
	require.Equal(t, "first", selected)
	_, err = repos.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: "first", UserID: "person"})
	require.NoError(t, err)
	selected, err = repos.AccountGrants.ResolveAccount(t.Context(), "person", "")
	require.NoError(t, err)
	require.Equal(t, "first", selected)
	_, err = repos.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: "second", UserID: "person"})
	require.NoError(t, err)
	_, err = repos.AccountGrants.ResolveAccount(t.Context(), "person", "")
	require.ErrorIs(t, err, identity.ErrAccountSelectionRequired)
	selected, err = repos.AccountGrants.ResolveAccount(t.Context(), "person", "second")
	require.NoError(t, err)
	require.Equal(t, "second", selected)
	_, err = repos.AccountGrants.ResolveAccount(t.Context(), "person", "foreign")
	require.ErrorIs(t, err, identity.ErrAccountGrantNotFound)
	require.NoError(t, repos.AccountGrants.Remove(t.Context(), identity.AccountGrant{AccountID: "first", UserID: "person"}))
	require.NoError(t, repos.Memberships.Remove(t.Context(), "person", "team"))
	_, err = repos.AccountGrants.ResolveAccount(t.Context(), "person", "first")
	require.ErrorIs(t, err, identity.ErrAccountGrantNotFound)
	_, err = repos.AccountGrants.ResolveAccount(t.Context(), "", "first")
	require.ErrorIs(t, err, identity.ErrMissingID)
}
