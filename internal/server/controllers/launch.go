package controllers

import (
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/localauth"
)

// LaunchController exchanges a launch ticket for a console session.
//
// It is the only route that turns something an operator can hold into
// something a browser can hold, and it exists so the browser never has to hold
// a gateway API key. A key pasted into a console is a long-lived tenant
// credential sitting in local storage, where a copied URL, a shared profile, or
// a browser extension can reach it and nothing can revoke it individually.
type LaunchController struct {
	gate *localauth.Gate
	// now is injected so a test can watch a ticket expire without waiting for
	// it. Production passes nil and gets time.Now.
	now func() time.Time
}

// NewLaunchController creates a controller over the running gateway's local
// admin token. A nil gate leaves the route mounted and refusing, which is what
// a deployment with no local token should do: 404 would say the feature does
// not exist in this build, and it does.
func NewLaunchController(gate *localauth.Gate) *LaunchController {
	return &LaunchController{gate: gate}
}

// launchRefusal is what a browser sees when a ticket does not work. It is one
// message for every cause on purpose: which check failed is a fact about the
// gateway's state, and a caller holding a ticket that did not work has no way
// to act on the difference. `starport ui` is the answer to all of them.
const launchRefusal = "This launch link did not work.\n\n" +
	"A launch link opens the console once and stops working after about a " +
	"minute, so a link you opened before, or one that has been sitting in a " +
	"terminal, is expected to fail here.\n\n" +
	"Run `starport ui` on the machine running this gateway for a fresh one.\n"

// Launch handles GET /launch.
//
// It redirects rather than rendering the console itself, so the ticket leaves
// the address bar the moment it is spent. A console served directly at this URL
// would keep a spent credential in history, in the tab title, and in whatever
// the operator copies next.
func (c *LaunchController) Launch(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get(localauth.TicketParam)
	if ticket == "" {
		c.refuse(w, r, nil)
		return
	}
	value, session, err := c.gate.Redeem(ticket, c.clock())
	if err != nil {
		c.refuse(w, r, err)
		return
	}
	for _, cookie := range localauth.SessionCookies(value, session, requestIsSecure(r)) {
		http.SetCookie(w, cookie)
	}
	log.Info().
		Str("ticket_prefix", localauth.TicketPrefix(ticket)).
		Time("session_expires_at", session.ExpiresAt).
		Msg("Opened a console session from a launch ticket")
	// 303 makes the browser follow with GET regardless of how it arrived, and
	// the console index is a page rather than a resource this route produced.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// refuse answers a ticket that did not work, and clears any session cookies the
// browser is carrying. A refusal reached with a stale cookie almost always
// means the token was rotated, and leaving the cookie in place would let the
// console keep claiming to be signed in while every request behind it fails.
func (c *LaunchController) refuse(w http.ResponseWriter, r *http.Request, cause error) {
	for _, cookie := range localauth.ClearedSessionCookies(requestIsSecure(r)) {
		http.SetCookie(w, cookie)
	}
	// The reason is logged and not sent. An operator debugging this is at the
	// terminal the gateway is running in.
	log.Warn().Err(cause).
		Str("ticket_prefix", localauth.TicketPrefix(r.URL.Query().Get(localauth.TicketParam))).
		Msg("Refused a launch ticket")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(launchRefusal))
}

func (c *LaunchController) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// requestIsSecure reports whether the browser reached this gateway over TLS.
//
// A Secure cookie set on a plain-HTTP loopback console is one the browser
// discards without saying so, and an operator would see a launch that reported
// success and a console that stayed signed out. A forwarded scheme is honoured
// because the common deployment behind TLS terminates it at a proxy, and the
// gateway's own listener is plain in exactly that case.
//
// The forwarded header is attacker-controllable where no proxy sets it, and
// that is harmless in this direction: the only thing a forged value can do is
// mark the caller's own cookie Secure, which withholds it from the caller's own
// plain-HTTP requests. There is no value of this header that loosens a cookie.
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}
