package keyring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
)

// newTestIdentity opens migrated identity repositories on an in-memory
// relational store, the same composition the runtime builds.
func newTestIdentity(t *testing.T) identity.Repositories {
	t.Helper()
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(context.Background()))
	repositories, err := identity.Open(db)
	require.NoError(t, err)
	return repositories
}

// TestAccountGrantedCredentialResolutionEndToEnd proves the whole grant
// chain across its two seams. In internal/identity, an AccountGrant gives an
// account to a user directly and another to a team the user is on, and the
// user's reachable accounts fold both. Here, a granted shared credential
// whose grant list names those accounts serves each reachable account and
// refuses an account no grant reaches — so who may use which account and
// which accounts a credential serves are the same facts end to end.
func TestAccountGrantedCredentialResolutionEndToEnd(t *testing.T) {
	ctx := context.Background()
	repositories := newTestIdentity(t)

	_, err := repositories.Users.Create(ctx, identity.User{ID: "u-1", Subject: "google:1"})
	require.NoError(t, err)
	_, err = repositories.Teams.Create(ctx, identity.Team{ID: "t-1", Name: "Platform"})
	require.NoError(t, err)
	_, err = repositories.Memberships.Add(ctx, identity.Membership{UserID: "u-1", TeamID: "t-1"})
	require.NoError(t, err)

	// One account reaches the user directly, one through the team.
	_, err = repositories.AccountGrants.Add(ctx, identity.AccountGrant{AccountID: "acct-direct", UserID: "u-1"})
	require.NoError(t, err)
	_, err = repositories.AccountGrants.Add(ctx, identity.AccountGrant{AccountID: "acct-team", TeamID: "t-1"})
	require.NoError(t, err)

	reachable, err := repositories.AccountGrants.ReachableAccounts(ctx, "u-1")
	require.NoError(t, err)
	require.Equal(t, []string{"acct-direct", "acct-team"}, reachable)

	// The shared credential is granted to exactly the accounts the user's
	// AccountGrant rows reach.
	provider := syntheticCredentialProvider()
	manager := newSyntheticProviderKeys(t, provider)
	_, err = manager.AddSharedCredential(ctx, string(provider.ID),
		map[string]string{"api-key": "secret-granted"}, nil,
		SharedCredentialParams{Access: credentials.AccessGranted, Grants: reachable})
	require.NoError(t, err)

	for _, accountID := range reachable {
		material, resolveErr := manager.ResolveSharedMaterial(ctx, accountID, provider)
		require.NoError(t, resolveErr, "account %s is reachable, so it spends the credential", accountID)
		value, exists := material.Value("api-key")
		require.True(t, exists)
		assert.Equal(t, "secret-granted", value)
	}

	// An account outside the user's grants gets nothing from the credential.
	_, err = manager.ResolveSharedMaterial(ctx, "acct-outside", provider)
	assert.ErrorIs(t, err, ErrKeyNotFound,
		"a granted shared credential serves only accounts its grants name")

	// Revoking the identity-side grant and restating the credential's list
	// from the new resolution closes the door for the revoked account: the
	// two seams stay one fact.
	require.NoError(t, repositories.AccountGrants.Remove(ctx,
		identity.AccountGrant{AccountID: "acct-direct", UserID: "u-1"}))
	reachable, err = repositories.AccountGrants.ReachableAccounts(ctx, "u-1")
	require.NoError(t, err)
	require.Equal(t, []string{"acct-team"}, reachable)

	listed, err := manager.GetSharedCredentials(ctx, string(provider.ID))
	require.NoError(t, err)
	require.Len(t, listed, 1)
	_, err = manager.UpdateSharedCredential(ctx, string(provider.ID), listed[0].ID,
		SharedCredentialUpdate{Grants: &reachable})
	require.NoError(t, err)

	_, err = manager.ResolveSharedMaterial(ctx, "acct-direct", provider)
	assert.ErrorIs(t, err, ErrKeyNotFound, "the revoked account no longer spends anything")
	_, err = manager.ResolveSharedMaterial(ctx, "acct-team", provider)
	assert.NoError(t, err, "the team-granted account still does")
}
