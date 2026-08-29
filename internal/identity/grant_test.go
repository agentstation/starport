package identity

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAccountGrantValidate(t *testing.T) {
	if err := (AccountGrant{AccountID: "acct-1", UserID: "u-1"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AccountGrant{AccountID: "acct-1", TeamID: "t-1"}).Validate(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		grant AccountGrant
		want  error
	}{
		{"missing account", AccountGrant{UserID: "u-1"}, ErrMissingID},
		{"oversized account", AccountGrant{AccountID: strings.Repeat("a", 192), UserID: "u-1"}, ErrMissingID},
		{"no grantee", AccountGrant{AccountID: "acct-1"}, ErrGranteeRequired},
		{"both grantees", AccountGrant{AccountID: "acct-1", UserID: "u-1", TeamID: "t-1"}, ErrGranteeRequired},
		{"oversized user", AccountGrant{AccountID: "acct-1", UserID: strings.Repeat("u", 192)}, ErrMissingID},
		{"oversized team", AccountGrant{AccountID: "acct-1", TeamID: strings.Repeat("t", 192)}, ErrMissingID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.grant.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// grantFixture seeds two users, one team with u-2 on it, and returns the
// repositories: the smallest population where user grants and team grants
// answer differently.
func grantFixture(t *testing.T) (Repositories, context.Context) {
	t.Helper()
	repositories := newTestRepositories(t)
	ctx := context.Background()
	if _, err := repositories.Users.Create(ctx, User{ID: "u-1", Subject: "google:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Users.Create(ctx, User{ID: "u-2", Subject: "google:2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Teams.Create(ctx, Team{ID: "t-1", Name: "Platform"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Memberships.Add(ctx, Membership{UserID: "u-2", TeamID: "t-1"}); err != nil {
		t.Fatal(err)
	}
	return repositories, ctx
}

func TestAccountGrantRepository(t *testing.T) {
	repositories, ctx := grantFixture(t)
	grants := repositories.AccountGrants

	if _, err := grants.Add(ctx, AccountGrant{AccountID: "acct-1", UserID: "ghost"}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("ghost user = %v", err)
	}
	if _, err := grants.Add(ctx, AccountGrant{AccountID: "acct-1", TeamID: "ghost"}); !errors.Is(err, ErrTeamNotFound) {
		t.Fatalf("ghost team = %v", err)
	}

	added, err := grants.Add(ctx, AccountGrant{AccountID: "acct-1", UserID: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if added.CreatedAt.IsZero() {
		t.Fatal("add must stamp creation time")
	}
	if _, err := grants.Add(ctx, AccountGrant{AccountID: "acct-1", UserID: "u-1"}); !errors.Is(err, ErrAccountGrantConflict) {
		t.Fatalf("duplicate grant = %v", err)
	}
	if _, err := grants.Add(ctx, AccountGrant{AccountID: "acct-1", TeamID: "t-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Add(ctx, AccountGrant{AccountID: "acct-2", UserID: "u-1"}); err != nil {
		t.Fatal(err)
	}

	byAccount, err := grants.ListByAccount(ctx, "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(byAccount) != 2 {
		t.Fatalf("by account = %+v", byAccount)
	}
	byUser, err := grants.ListByUser(ctx, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(byUser) != 2 || byUser[0].AccountID != "acct-1" || byUser[1].AccountID != "acct-2" {
		t.Fatalf("by user = %+v", byUser)
	}
	byTeam, err := grants.ListByTeam(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(byTeam) != 1 || byTeam[0].AccountID != "acct-1" {
		t.Fatalf("by team = %+v", byTeam)
	}

	if err := grants.Remove(ctx, AccountGrant{AccountID: "acct-1", UserID: "u-1"}); err != nil {
		t.Fatal(err)
	}
	if err := grants.Remove(ctx, AccountGrant{AccountID: "acct-1", UserID: "u-1"}); !errors.Is(err, ErrAccountGrantNotFound) {
		t.Fatalf("remove absent grant = %v", err)
	}
	if err := grants.Remove(ctx, AccountGrant{AccountID: "acct-1"}); !errors.Is(err, ErrGranteeRequired) {
		t.Fatalf("remove without grantee = %v", err)
	}
}

// TestReachableAccountsFoldGrants proves the resolution the session seam
// rides: a user reaches the accounts granted to them directly and the ones
// granted to their teams, once each, in order.
func TestReachableAccountsFoldGrants(t *testing.T) {
	repositories, ctx := grantFixture(t)
	grants := repositories.AccountGrants

	seed := []AccountGrant{
		{AccountID: "acct-direct", UserID: "u-2"},
		{AccountID: "acct-team", TeamID: "t-1"},
		// Granted both ways: must appear once.
		{AccountID: "acct-both", UserID: "u-2"},
		{AccountID: "acct-both", TeamID: "t-1"},
		// Someone else's grant: must not appear.
		{AccountID: "acct-other", UserID: "u-1"},
	}
	for _, grant := range seed {
		if _, err := grants.Add(ctx, grant); err != nil {
			t.Fatal(err)
		}
	}

	reachable, err := grants.ReachableAccounts(ctx, "u-2")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acct-both", "acct-direct", "acct-team"}; !reflect.DeepEqual(reachable, want) {
		t.Fatalf("reachable = %v, want %v", reachable, want)
	}

	// u-1 is on no team: only the direct grant answers.
	reachable, err = grants.ReachableAccounts(ctx, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acct-other"}; !reflect.DeepEqual(reachable, want) {
		t.Fatalf("reachable = %v, want %v", reachable, want)
	}
}

// TestDeletingAGranteeRemovesItsGrants holds the cascade: grants are access
// control, so none may outlive the user or team it names.
func TestDeletingAGranteeRemovesItsGrants(t *testing.T) {
	repositories, ctx := grantFixture(t)
	grants := repositories.AccountGrants

	if _, err := grants.Add(ctx, AccountGrant{AccountID: "acct-1", UserID: "u-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Add(ctx, AccountGrant{AccountID: "acct-1", TeamID: "t-1"}); err != nil {
		t.Fatal(err)
	}

	if err := repositories.Users.Delete(ctx, "u-1", 0); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Teams.Delete(ctx, "t-1", 0); err != nil {
		t.Fatal(err)
	}

	remaining, err := grants.ListByAccount(ctx, "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("grants survived their grantees: %+v", remaining)
	}
}

// TestAccountResolverResolvesASubject proves the object the composition root
// hands the session gate: the subject an identity session carries comes back
// as the accounts the person's grants reach.
func TestAccountResolverResolvesASubject(t *testing.T) {
	repositories, ctx := grantFixture(t)
	resolver, err := NewAccountResolver(repositories.Users, repositories.AccountGrants)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repositories.AccountGrants.Add(ctx, AccountGrant{AccountID: "acct-direct", UserID: "u-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.AccountGrants.Add(ctx, AccountGrant{AccountID: "acct-team", TeamID: "t-1"}); err != nil {
		t.Fatal(err)
	}

	reachable, err := resolver.ReachableAccounts(ctx, "google:2")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acct-direct", "acct-team"}; !reflect.DeepEqual(reachable, want) {
		t.Fatalf("reachable = %v, want %v", reachable, want)
	}

	// A subject whose user is gone reaches nothing, and that is an answer,
	// not an error: the session outlived the user, so it has no accounts.
	gone, err := resolver.ReachableAccounts(ctx, "google:nobody")
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 0 {
		t.Fatalf("unknown subject reached %v", gone)
	}

	if _, err := NewAccountResolver(nil, nil); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("nil repositories = %v", err)
	}
}
