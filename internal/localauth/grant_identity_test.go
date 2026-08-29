package localauth

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// providerStub stands in for a configured acquisition path. It exists so the
// wired path is proved rather than described: a seam whose only tested state is
// "refuses" is a seam nobody has checked can be filled.
type providerStub struct {
	subject string
	err     error
}

func (p providerStub) Authenticate(string) (string, error) { return p.subject, p.err }

// TestAnUnconfiguredGatewayRefusesIdentitySignIn holds the default state, and
// holds it as two separate facts. The grant has to be *registered* — otherwise
// adding a provider means reopening the seam — and it has to refuse with the
// error that says "not configured here" rather than the one that says "no such
// thing", because those are different answers to give an operator.
func TestAnUnconfiguredGatewayRefusesIdentitySignIn(t *testing.T) {
	gate := NewGate(testToken(t, 1), "127.0.0.1")

	grant, err := gate.Grant(GrantIdentity)
	require.NoError(t, err, "the identity grant ships registered, not absent")
	require.Equal(t, GrantIdentity, grant.Kind())

	_, _, err = gate.MintSession(GrantIdentity, GrantRequest{Claim: "any-code"}, time.Now())
	require.ErrorIs(t, err, ErrIdentityProviderNotConfigured)
	require.NotErrorIs(t, err, ErrGrantUnknown)
}

// TestUseIdentityProviderFillsTheSlot proves the composition root's one call
// turns the inert grant into real sign-in, through the same registered grant
// and the same minting path every other grant uses.
func TestUseIdentityProviderFillsTheSlot(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token, "127.0.0.1")
	gate.UseIdentityProvider(providerStub{subject: "google:114380"})

	now := time.Now()
	cookie, session, err := gate.MintSession(GrantIdentity, GrantRequest{Claim: "a-claim"}, now)
	require.NoError(t, err)
	require.Equal(t, GrantIdentity, session.Grant)
	require.Equal(t, "google:114380", session.Subject)

	verified, err := gate.Verify(cookie, now)
	require.NoError(t, err)
	require.Equal(t, "google:114380", verified.Subject)

	// A nil provider is a no-op, not a reset: nothing may empty a filled slot
	// back to inert by accident.
	gate.UseIdentityProvider(nil)
	_, _, err = gate.MintSession(GrantIdentity, GrantRequest{Claim: "a-claim"}, now)
	require.NotErrorIs(t, err, ErrIdentityProviderNotConfigured)
}

// TestAProviderVouchedSessionCarriesItsSubject proves the seam can be filled.
// The subject has to survive the signed round trip, or an enterprise deployment
// would authenticate a person and then hold a session that has forgotten who
// they are.
func TestAProviderVouchedSessionCarriesItsSubject(t *testing.T) {
	token := testToken(t, 1)
	grant := &identityGrant{token: token, provider: providerStub{subject: "auth0|9f3c"}}
	now := time.Now()

	cookie, session, err := grant.Mint(GrantRequest{Claim: "an-authorization-code"}, now)
	require.NoError(t, err)
	require.Equal(t, GrantIdentity, session.Grant)
	require.Equal(t, "auth0|9f3c", session.Subject)

	verified, err := VerifySession(cookie, token, now)
	require.NoError(t, err)
	require.Equal(t, GrantIdentity, verified.Grant)
	require.Equal(t, "auth0|9f3c", verified.Subject)
}

// TestAProviderThatNamesNobodyIsRefused covers the failure mode a provider is
// most likely to have: authenticating successfully and returning an empty
// subject. Admitting that would mint a session that claims to know who the
// caller is and names nobody, which is strictly worse than refusing.
func TestAProviderThatNamesNobodyIsRefused(t *testing.T) {
	token := testToken(t, 1)

	for name, subject := range map[string]string{
		"an empty subject": "",
		"whitespace":       "  \t ",
	} {
		t.Run(name, func(t *testing.T) {
			grant := &identityGrant{token: token, provider: providerStub{subject: subject}}
			_, _, err := grant.Mint(GrantRequest{Claim: "code"}, time.Now())
			require.ErrorIs(t, err, ErrIdentitySubjectMissing)
		})
	}
}

// TestAProviderRefusalReachesTheCaller checks that this package does not
// swallow or reinterpret a provider's own refusal. A provider knows why it said
// no — expired code, wrong audience, revoked account — and translating that
// into one of this package's errors would lose the only useful part.
func TestAProviderRefusalReachesTheCaller(t *testing.T) {
	refused := errors.New("the authorization code has expired")
	grant := &identityGrant{
		token:    testToken(t, 1),
		provider: providerStub{err: refused},
	}

	_, _, err := grant.Mint(GrantRequest{Claim: "stale-code"}, time.Now())
	require.ErrorIs(t, err, refused)
	require.NotErrorIs(t, err, ErrIdentitySubjectMissing)
}

// TestTheSubjectAndTheGrantMustAgree is the invariant that keeps the two ideas
// apart once both exist in the same cookie.
//
// A machine-local grant proves the caller can reach something only a process on
// this machine could hand them. It does not know who they are, and a cookie
// that let it carry a subject anyway would turn "where you are" into "who you
// are" — which is exactly the conflation this campaign exists to prevent, now
// expressible in a signed value.
//
// Both cookies below are correctly signed by this machine's token. The
// signature is not the thing being tested; the pairing is.
func TestTheSubjectAndTheGrantMustAgree(t *testing.T) {
	token := testToken(t, 1)
	now := time.Now()

	forge := func(t *testing.T, grant GrantKind, subject string) string {
		t.Helper()
		payload, err := json.Marshal(sessionPayload{
			IssuedAt:  now.UnixMilli(),
			ExpiresAt: now.Add(SessionTTL).UnixMilli(),
			Grant:     grant,
			Subject:   subject,
		})
		require.NoError(t, err)
		return sign(token, sessionPurpose, payload)
	}

	for name, forged := range map[string]string{
		"a ticket that claims a person":       forge(t, GrantTicket, "auth0|9f3c"),
		"a pasted token that claims a person": forge(t, GrantLocalToken, "auth0|9f3c"),
		"an identity session naming nobody":   forge(t, GrantIdentity, ""),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := VerifySession(forged, token, now)
			require.ErrorIs(t, err, ErrSessionMalformed)
		})
	}

	// The same rule at the other end: the exported entry point cannot mint an
	// identity session, because it has no subject to give one.
	_, _, err := IssueSession(token, GrantIdentity, now)
	require.ErrorIs(t, err, ErrIdentitySubjectMissing)
}
