package localauth

import (
	"errors"
	"fmt"
	"time"
)

// GrantKind names one registered way to mint a console session.
//
// The three kinds are not variations on a theme. A launch ticket and the local
// admin token are claims about where the caller is: both say "this browser can
// reach something only a process on this machine could hand it", and neither
// says who is holding it. An identity provider says the opposite — it names a
// person and says nothing about the machine.
//
// The distinction is why this package spends a type on it. A session that
// records which kind minted it can be logged, tested, and later restricted
// without a second lookup, and the vocabulary keeps the word "sign in"
// attached to the only grant that earns it.
type GrantKind string

const (
	// GrantTicket is the one-time launch ticket the CLI hands the browser.
	GrantTicket GrantKind = "ticket"

	// GrantLocalToken is the local admin token pasted into the console.
	GrantLocalToken GrantKind = "local-token"

	// GrantIdentity is the seam an identity provider fills. No provider ships,
	// and the grant refuses until one is configured. It is registered rather
	// than absent so that adding a provider is filling a slot instead of
	// reopening this seam, and so its refusal is a tested state rather than a
	// comment.
	GrantIdentity GrantKind = "identity"
)

// ErrGrantUnknown reports a grant kind this gateway does not register. It
// answers a request for a grant that does not exist and a session cookie whose
// recorded grant this version cannot read.
var ErrGrantUnknown = errors.New("no such console session grant")

// GrantRequest is everything a grant may know about the caller.
//
// It carries a host string rather than an *http.Request on purpose. A grant
// decides what to believe about a claim; deciding it from headers, cookies, or
// a URL would make this package a second HTTP layer, and the one fact a grant
// genuinely needs about the transport is where the caller is.
type GrantRequest struct {
	// Claim is whatever that grant reads: a ticket, a pasted secret, or a
	// provider's callback code.
	Claim string

	// RemoteHost is the host the request arrived from, without a port. It is
	// empty for an in-process caller, which a grant reads as "this machine".
	RemoteHost string
}

// Grant turns one caller's claim into a console session.
//
// Every implementation ends at IssueSession. That is the point of the
// interface: a grant chooses what to believe, and nothing else. It does not
// choose the cookie shape, the session lifetime, or the refusal text, because
// a grant that could choose those would eventually choose them differently.
type Grant interface {
	// Kind reports which grant this is. The value reaches the session, so it
	// has to agree with the key the gate registered it under.
	Kind() GrantKind

	// Mint turns a request into a cookie value and the session it stands for.
	Mint(request GrantRequest, now time.Time) (string, Session, error)
}

// grantKinds lists every kind this binary knows, in layering order. A session
// cookie carrying anything else is refused rather than honoured, so a value
// signed by a future version cannot be read as one of these.
var grantKinds = []GrantKind{GrantTicket, GrantLocalToken, GrantIdentity}

// knownGrantKind reports whether kind is one this binary can act on.
func knownGrantKind(kind GrantKind) bool {
	for _, known := range grantKinds {
		if known == kind {
			return true
		}
	}
	return false
}

// ticketGrant redeems a launch ticket. It is the grant the CLI drives when it
// opens a browser, and it holds the ticket store because a one-time ticket is
// only one-time if something remembers.
type ticketGrant struct {
	token   Token
	tickets *Tickets
}

func (g ticketGrant) Kind() GrantKind { return GrantTicket }

// Mint reads only the claim. A ticket is single-use and short-lived and was
// handed to this browser by a process on this machine, so where the browser
// then presented it adds nothing the ticket does not already prove.
func (g ticketGrant) Mint(request GrantRequest, now time.Time) (string, Session, error) {
	if g.tickets == nil {
		return "", Session{}, fmt.Errorf("%w: %s", ErrGrantUnknown, GrantTicket)
	}
	if err := g.tickets.Redeem(request.Claim, g.token, now); err != nil {
		return "", Session{}, err
	}
	return IssueSession(g.token, GrantTicket, now)
}
