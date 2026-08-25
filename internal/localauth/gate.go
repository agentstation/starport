package localauth

import "time"

// Gate is the running gateway's half of the browser flow: it redeems launch
// tickets and verifies console sessions against one local admin token.
//
// It exists so the secret has one holder inside the server. The HTTP adapter
// carries a Gate rather than a Token, so no configuration struct, dependency
// struct, or controller field holds the credential, and nothing downstream can
// print it by printing itself.
//
// The token is the one read at startup. A rotation writes a new file and does
// not reach a running process, which is what `starport auth rotate` says out
// loud: the sessions this Gate issued keep working until the gateway restarts,
// and then none of them do.
type Gate struct {
	token   Token
	tickets *Tickets
}

// NewGate returns a gate over one token. A zero Token yields a gate that
// refuses everything, because an unvalidatable token signs nothing.
func NewGate(token Token) *Gate {
	return &Gate{token: token, tickets: NewTickets()}
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

// Redeem spends a launch ticket and returns the cookie value for the session it
// opens. Every rejection is one of this package's exported errors.
func (g *Gate) Redeem(ticket string, now time.Time) (string, Session, error) {
	if g == nil {
		return "", Session{}, ErrBadSignature
	}
	if err := g.tickets.Redeem(ticket, g.token, now); err != nil {
		return "", Session{}, err
	}
	return IssueSession(g.token, now)
}

// Verify reports the session a cookie value stands for, or why it does not.
func (g *Gate) Verify(cookie string, now time.Time) (Session, error) {
	if g == nil {
		return Session{}, ErrBadSignature
	}
	return VerifySession(cookie, g.token, now)
}
