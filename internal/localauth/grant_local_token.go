package localauth

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// GatewayAPIKeyPrefix is how a gateway API key announces itself.
//
// This package does not issue those keys and never accepts one. It knows the
// prefix for one reason: to tell a reader who pasted the wrong credential
// which one this field wants, instead of answering with the refusal it gives
// a wrong secret.
const GatewayAPIKeyPrefix = "STARPORT_"

var (
	// ErrTokenRejected reports a value that is not this machine's local admin
	// token. It is the answer to a wrong secret, an empty one, and a
	// well-formed guess alike, for the reason ErrBadSignature is: distinguishing
	// them would say whether the guess had the right shape.
	ErrTokenRejected = errors.New("the value is not this machine's local admin token")

	// ErrGatewayAPIKeyPresented reports a gateway API key offered where the
	// local admin token belongs.
	//
	// It is deliberately a distinct answer. The two credentials are different
	// ideas — one authenticates an account's inference request, the other is a
	// claim about sitting at this machine — and a reader who has confused them
	// learns nothing from a refusal that also covers a wrong secret. Naming the
	// mistake narrows no search space, because the prefix the reader typed is
	// already public.
	ErrGatewayAPIKeyPresented = errors.New(
		"that is a gateway API key; this field takes the local admin token from " + TokenCommand,
	)

	// ErrRemoteTokenRefused reports a pasted token presented from somewhere
	// other than this machine while the token is the one first boot printed.
	ErrRemoteTokenRefused = errors.New(
		"a local admin token is only accepted from this machine until it has been rotated",
	)
)

// TokenCommand names the command that prints the local admin token. The
// refusal above points at it, and the console shows it, so both say the same
// words.
const TokenCommand = "starport auth token" // #nosec G101 -- A command line for an operator to type, not credential material.

// failedAttemptDelay is how long a rejected paste holds the lock.
//
// It is not a brute-force defense: the secret is 32 random bytes, and no
// delay makes that guessable or fails to. It is here so a script hammering
// the route runs at a few attempts per second instead of thousands, which
// keeps the log readable and keeps one caller from turning the route into a
// way to spend the gateway's CPU.
//
// There is deliberately no lockout. A lockout on a single-operator gateway is
// a way for anyone who can reach the port to lock the operator out of their
// own console, which trades a threat that does not exist for one that does.
const failedAttemptDelay = 250 * time.Millisecond

// localTokenGrant admits a browser that presents this machine's local admin
// token.
//
// It exists because `starport auth token` prints a value, and a value the
// product prints and then accepts nowhere is a value that teaches an operator
// the product is broken.
type localTokenGrant struct {
	token    Token
	bindHost string

	// mu serializes rejected attempts. Serializing is the point: a per-caller
	// delay is one an attacker sidesteps by opening more connections.
	mu    sync.Mutex
	delay time.Duration
	sleep func(time.Duration)
}

func newLocalTokenGrant(token Token, bindHost string) *localTokenGrant {
	return &localTokenGrant{
		token:    token,
		bindHost: bindHost,
		delay:    failedAttemptDelay,
		sleep:    time.Sleep,
	}
}

func (g *localTokenGrant) Kind() GrantKind { return GrantLocalToken }

// Mint admits a caller that presents the token this gateway holds.
func (g *localTokenGrant) Mint(request GrantRequest, now time.Time) (string, Session, error) {
	candidate := strings.TrimSpace(request.Claim)
	if strings.HasPrefix(candidate, GatewayAPIKeyPrefix) {
		return "", Session{}, ErrGatewayAPIKeyPresented
	}
	// Where the caller is, not where the process bound. The startup check in
	// internal/app reads the configured bind host, and a reverse proxy in front
	// of a loopback gateway satisfies it while making this route reachable from
	// the network. A first-boot secret has been in a terminal, and a terminal is
	// scrollback, a tmux buffer, a screen share, and a CI log.
	if !AllowsExposure(g.origin(request), g.token) {
		return "", Session{}, ErrRemoteTokenRefused
	}
	if !g.token.Authorizes(candidate) {
		g.throttle()
		return "", Session{}, ErrTokenRejected
	}
	return IssueSession(g.token, GrantLocalToken, now)
}

// origin is the host this grant judges. An empty RemoteHost means an
// in-process caller, which is this machine by definition; anything else is the
// address the request actually arrived from.
func (g *localTokenGrant) origin(request GrantRequest) string {
	if request.RemoteHost == "" {
		return g.bindHost
	}
	return request.RemoteHost
}

func (g *localTokenGrant) throttle() {
	if g.delay <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sleep(g.delay)
}
