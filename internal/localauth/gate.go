package localauth

import (
	"fmt"
	"time"
)

// Gate is the running gateway's half of the browser flow: it mints console
// sessions through its registered grants and verifies them against one local
// admin token.
//
// It exists so the secret has one holder inside the server. The HTTP adapter
// carries a Gate rather than a Token, so no configuration struct, dependency
// struct, or controller field holds the credential, and nothing downstream can
// print it by printing itself.
//
// The grants are a registry rather than a switch on a kind. A caller asks for
// a grant by name and gets a refusal for one that is not registered, which is
// how the identity grant can ship present and inert: its absence is a value
// the gate returns rather than a branch nobody wrote.
//
// The token is the one read at startup. A rotation writes a new file and does
// not reach a running process, which is what `starport auth rotate` says out
// loud: the sessions this Gate issued keep working until the gateway restarts,
// and then none of them do.
type Gate struct {
	token   Token
	tickets *Tickets
	grants  map[GrantKind]Grant
}

// NewGate returns a gate over one token. A zero Token yields a gate that
// refuses everything, because an unvalidatable token signs nothing.
//
// bindHost is the address the gateway serves on. It is the fallback origin for
// a grant that judges where a caller is, used when the caller is in-process
// and so has no remote address of its own.
func NewGate(token Token, bindHost string) *Gate {
	tickets := NewTickets()
	gate := &Gate{token: token, tickets: tickets, grants: map[GrantKind]Grant{}}
	gate.register(ticketGrant{token: token, tickets: tickets})
	gate.register(newLocalTokenGrant(token, bindHost))
	// Registered with no provider, so asking for it answers
	// ErrIdentityProviderNotConfigured rather than "no such grant". Nothing in
	// this repository supplies one.
	gate.register(newIdentityGrant(token))
	return gate
}

// register files a grant under the kind it reports. A grant registered under a
// name it does not claim would put one value in the session and another in the
// log, so the grant's own answer is the key.
func (g *Gate) register(grant Grant) {
	g.grants[grant.Kind()] = grant
}

// Grant returns the registered grant for kind, or ErrGrantUnknown.
func (g *Gate) Grant(kind GrantKind) (Grant, error) {
	if g == nil || g.grants == nil {
		return nil, ErrGrantUnknown
	}
	grant, found := g.grants[kind]
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrGrantUnknown, kind)
	}
	return grant, nil
}

// MintSession runs one grant over one request and returns the cookie value for
// the session it opens.
//
// Every caller-facing path into a console session goes through here, so the
// cookie shape, the lifetime, and the recorded grant have one origin. Every
// rejection is one of this package's exported errors.
func (g *Gate) MintSession(
	kind GrantKind, request GrantRequest, now time.Time,
) (string, Session, error) {
	grant, err := g.Grant(kind)
	if err != nil {
		return "", Session{}, err
	}
	return grant.Mint(request, now)
}

// Generation reports which token this gate holds. It is safe to log and is the
// value an operator compares against `starport auth status` when a session
// stops working after a rotation.
func (g *Gate) Generation() uint64 {
	if g == nil {
		return 0
	}
	return g.token.Generation
}

// MintTicket issues a launch ticket from the token this gate holds. It is what
// an in-process launch uses — `starport dev` already has the running gateway,
// so it has no reason to go back to the file.
func (g *Gate) MintTicket(now time.Time) (string, error) {
	if g == nil {
		return "", ErrPathRequired
	}
	return MintTicket(g.token, now)
}

// Redeem spends a launch ticket and returns the cookie value for the session
// it opens. It is the ticket grant under the name the launch route has always
// called it.
func (g *Gate) Redeem(ticket string, now time.Time) (string, Session, error) {
	if g == nil {
		return "", Session{}, ErrBadSignature
	}
	return g.MintSession(GrantTicket, GrantRequest{Claim: ticket}, now)
}

// Verify reports the session a cookie value stands for, or why it does not.
func (g *Gate) Verify(cookie string, now time.Time) (Session, error) {
	if g == nil {
		return Session{}, ErrBadSignature
	}
	return VerifySession(cookie, g.token, now)
}
