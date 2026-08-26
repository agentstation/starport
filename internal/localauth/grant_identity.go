package localauth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrIdentityProviderNotConfigured reports the shipped state: the identity
	// grant is registered and no provider fills it.
	//
	// It is a distinct error rather than ErrGrantUnknown because the two mean
	// different things to whoever is reading. "No such grant" says this gateway
	// has never heard of identity sign-in; this says the seam is here and the
	// deployment has not filled it, which is an operator's answer to give.
	ErrIdentityProviderNotConfigured = errors.New(
		"no identity provider is configured for this gateway",
	)

	// ErrIdentitySubjectMissing reports a provider that authenticated a caller
	// without naming one. A session from this grant exists to carry a person, so
	// an empty subject is a broken provider rather than an anonymous success.
	ErrIdentitySubjectMissing = errors.New(
		"the identity provider named no subject",
	)
)

// IdentityProvider is the contract an enterprise deployment fills.
//
// It is one method because that is the whole of what this package needs to
// know. Where the claim came from — an OIDC authorization code, a SAML
// assertion, a header a trusted proxy set — is the provider's problem, and a
// provider that made this package understand any of it would put protocol
// details in the layer that decides what to believe.
//
// What a provider must supply is a stable subject: the identifier that will be
// the same person on the next sign-in. It reaches Session.Subject and is signed
// into the cookie, so it must be an identifier a deployment is willing to have
// in a browser and in a log — a provider's subject claim rather than a name or
// an address.
//
// What a provider must not do is decide the session. Lifetime, cookie shape,
// and refusal handling belong to this package for every grant, so an identity
// session cannot quietly outlive a machine-local one.
type IdentityProvider interface {
	// Authenticate turns a provider's callback claim into the subject it names.
	// The error is returned to the caller wrapped, never inspected: a provider
	// knows why it refused and this package does not.
	Authenticate(claim string) (string, error)
}

// identityGrant admits a browser an identity provider vouched for.
//
// It ships registered and inert. That is deliberate: an unregistered grant is a
// refactor waiting to happen, and the two machine-local grants would grow into
// the enterprise case by accident and take the word "sign in" with them. A
// registered grant that refuses with a named error is a state a test can hold,
// so adding a provider fills a slot instead of reopening this seam.
type identityGrant struct {
	token Token

	// provider is nil in every shipped build. Nothing in this repository sets
	// it, and identity_test.go asserts that, so the inert state is a decision
	// rather than an omission.
	provider IdentityProvider
}

func newIdentityGrant(token Token) *identityGrant {
	return &identityGrant{token: token}
}

func (g *identityGrant) Kind() GrantKind { return GrantIdentity }

// Mint authenticates through the configured provider, or reports that there is
// none.
func (g *identityGrant) Mint(request GrantRequest, now time.Time) (string, Session, error) {
	if g.provider == nil {
		return "", Session{}, ErrIdentityProviderNotConfigured
	}
	// Deliberately not throttled and not host-checked. Both of those controls
	// exist because a machine-local grant reads a secret this gateway printed;
	// an identity grant reads a claim the provider issued, and the provider owns
	// how many times a person may get it wrong and from where.
	subject, err := g.provider.Authenticate(request.Claim)
	if err != nil {
		return "", Session{}, fmt.Errorf("authenticate through the identity provider: %w", err)
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", Session{}, ErrIdentitySubjectMissing
	}
	return issueIdentitySession(g.token, subject, now)
}
