package controllers

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/localauth"
)

// ConsoleSessionController opens a console session from a token an operator
// pasted.
//
// It is the second way in, and it exists because the first one is not always
// available. A launch ticket is handed to a browser by a process on this
// machine; an operator who closed that tab, or who is looking at a gateway
// somebody else started, has no way back that does not involve restarting it.
// `starport auth token` prints a value for exactly this, and until this route
// existed nothing accepted that value.
type ConsoleSessionController struct {
	gate *localauth.Gate
	// now is injected so a test can control session expiry. Production passes
	// nil and gets time.Now.
	now func() time.Time
}

// NewConsoleSessionController creates a controller over the running gateway's
// local admin token. A nil gate leaves the route mounted and refusing, matching
// the launch route: 404 would say this build has no console sign-in, and it
// does.
func NewConsoleSessionController(gate *localauth.Gate) *ConsoleSessionController {
	return &ConsoleSessionController{gate: gate}
}

// sessionRefusal is what a browser sees when a pasted value does not work.
//
// It is one message for every cause, for the reason launchRefusal is: which
// check failed is a fact about the gateway's state, and a caller holding a
// value that did not work cannot act on the difference. Separating "wrong
// secret" from "right shape, wrong bytes" would tell a guesser whether they
// were getting warmer.
const sessionRefusal = "That value did not open a console session.\n\n" +
	"Run `starport auth token` on the machine running this gateway and paste " +
	"the value it prints.\n"

// wrongCredentialRefusal is the one deliberate exception.
//
// A gateway API key is a category error rather than a guess. The prefix the
// reader typed is already public, so naming it narrows no search space, and
// leaving somebody to work out which of two credentials a field wants is the
// confusion this route exists to remove.
const wrongCredentialRefusal = "That is a gateway API key, which authenticates " +
	"an API request rather than a person at this machine.\n\n" +
	"This field takes the local admin token. Run `starport auth token` on the " +
	"machine running this gateway.\n"

// sessionRequest is the body this route reads. The value is never logged and
// never echoed.
type sessionRequest struct {
	Token string `json:"token"`
}

// Create handles POST /console/session.
//
// The token arrives in a JSON body rather than a query string or a form field
// so it stays out of the address bar, out of browser history, and out of the
// access log line that records the URL. The response carries no body on
// success: everything the browser needs is in the cookies, and a body echoing
// anything about the credential is a body that ends up somewhere.
func (c *ConsoleSessionController) Create(w http.ResponseWriter, r *http.Request) {
	var body sessionRequest
	// A body larger than a token is not a token. The limit is generous enough
	// for any plausible secret and small enough that this route cannot be used
	// to make the gateway read something.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		c.refuse(w, r, err)
		return
	}

	value, session, err := c.gate.MintSession(
		localauth.GrantLocalToken,
		localauth.GrantRequest{Claim: body.Token, RemoteHost: callerHost(r)},
		c.clock(),
	)
	if err != nil {
		c.refuse(w, r, err)
		return
	}

	for _, cookie := range localauth.SessionCookies(value, session, requestIsSecure(r)) {
		http.SetCookie(w, cookie)
	}
	log.Info().
		Str("grant", string(session.Grant)).
		Time("session_expires_at", session.ExpiresAt).
		Msg("Opened a console session from a pasted local admin token")
	w.WriteHeader(http.StatusNoContent)
}

// refuse answers a value that did not work, and clears any session cookies the
// browser is carrying.
//
// Clearing matters here for the same reason it does on the launch route: a
// refusal reached with a stale cookie almost always means the token was
// rotated, and leaving the cookie would let the console keep claiming to be
// signed in while every request behind it fails.
func (c *ConsoleSessionController) refuse(w http.ResponseWriter, r *http.Request, cause error) {
	for _, cookie := range localauth.ClearedSessionCookies(requestIsSecure(r)) {
		http.SetCookie(w, cookie)
	}
	// The cause is logged and not sent. An operator debugging this is at the
	// terminal the gateway is running in. The pasted value is not logged at all,
	// not even a prefix: unlike a launch ticket it is long-lived, and a log line
	// is a place a long-lived secret should never reach.
	log.Warn().Err(cause).Msg("Refused a console session request")

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	message := sessionRefusal
	if errors.Is(cause, localauth.ErrGatewayAPIKeyPresented) {
		message = wrongCredentialRefusal
	}
	_ = json.NewEncoder(w).Encode(map[string]string{responseMessageField: message})
}

// callerHost is the address this request actually arrived from.
//
// It reads RemoteAddr rather than a forwarded header, and rather than the
// client IP the middleware chain records, for two separate reasons. A forwarded
// header is written by whoever is upstream, and on this route the answer
// decides whether an unrotated first-boot secret is accepted — so a value a
// caller can set is a value that defeats the check. And reading RemoteAddr
// directly means the control does not depend on a middleware being installed in
// the right order, which is the kind of coupling that fails quietly.
func callerHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// An address with no port is already a host. An unparseable one is
		// returned as it stands, which the exposure check reads as "not this
		// machine" — the safe answer when the transport cannot say where a
		// caller is.
		return r.RemoteAddr
	}
	return host
}

func (c *ConsoleSessionController) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
