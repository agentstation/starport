package controllers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/localauth"
)

// IdentityAuthenticator is the slice of the OAuth acquisition path these
// routes need. It is declared here so the controller can be tested against a
// stub and so this package depends on the contract rather than the
// acquisition machinery.
type IdentityAuthenticator interface {
	// Providers reports the configured provider names for the console to
	// offer.
	Providers() []string
	// Begin redirects the browser to the named provider's consent page.
	Begin(w http.ResponseWriter, r *http.Request, provider string) error
	// Complete verifies the provider's callback and returns the one-time
	// claim the identity grant redeems.
	Complete(w http.ResponseWriter, r *http.Request, provider string) (string, error)
}

// ConsoleIdentityController serves the third way into a console session: a
// person an identity provider vouched for. The two machine-local grants say
// where the caller is; this one says who they are, and it exists only when
// an operator configured a provider.
type ConsoleIdentityController struct {
	authenticator IdentityAuthenticator
	gate          *localauth.Gate
	// now is injected so a test can control session expiry. Production passes
	// nil and gets time.Now.
	now func() time.Time
}

// NewConsoleIdentityController creates the controller. A nil authenticator
// leaves the routes mounted and refusing with the operator's answer — no
// identity provider is configured — matching how every other optional
// surface degrades loudly instead of vanishing.
func NewConsoleIdentityController(
	authenticator IdentityAuthenticator, gate *localauth.Gate,
) *ConsoleIdentityController {
	return &ConsoleIdentityController{authenticator: authenticator, gate: gate}
}

// identityUnconfigured is what a caller reads on a deployment with no
// identity provider. It is the operator's answer, so it names the operator's
// lever.
const identityUnconfigured = "No identity provider is configured for this gateway.\n\n" +
	"An operator turns one on with the STARPORT_IDENTITY_* settings.\n"

// identityRefusal is the one message for a dance that did not complete. The
// cause is logged for the operator; a browser mid-redirect can only start
// over, and detailing which check failed would tell an attacker which forgery
// almost worked.
const identityRefusal = "That did not open a console session. Start again from the console.\n"

// Providers handles GET /console/identity/providers. An unconfigured
// deployment answers an empty list with 200: the console asks this on first
// contact, and "none" is a normal answer there, not a failure.
func (c *ConsoleIdentityController) Providers(w http.ResponseWriter, _ *http.Request) {
	names := []string{}
	if c.authenticator != nil {
		names = c.authenticator.Providers()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string][]string{providersField: names})
}

// Begin handles GET /console/identity/{provider}: it sends the browser to
// the provider's consent page.
func (c *ConsoleIdentityController) Begin(w http.ResponseWriter, r *http.Request) {
	if c.authenticator == nil {
		c.unconfigured(w)
		return
	}
	if err := c.authenticator.Begin(w, r, chi.URLParam(r, "provider")); err != nil {
		c.refuse(w, r, err)
	}
}

// Callback handles GET /console/identity/{provider}/callback: the provider
// sent the browser back, the acquisition path verifies the claim, and the
// identity grant turns it into the same console session every other grant
// mints.
func (c *ConsoleIdentityController) Callback(w http.ResponseWriter, r *http.Request) {
	if c.authenticator == nil {
		c.unconfigured(w)
		return
	}
	provider := chi.URLParam(r, "provider")
	claim, err := c.authenticator.Complete(w, r, provider)
	if err != nil {
		c.refuse(w, r, err)
		return
	}
	value, session, err := c.gate.MintSession(
		localauth.GrantIdentity, localauth.GrantRequest{Claim: claim}, c.clock())
	if err != nil {
		c.refuse(w, r, err)
		return
	}
	for _, cookie := range localauth.SessionCookies(value, session, requestIsSecure(r)) {
		http.SetCookie(w, cookie)
	}
	log.Info().
		Str("grant", string(session.Grant)).
		Str("provider", provider).
		Time("session_expires_at", session.ExpiresAt).
		Msg("Opened a console session through an identity provider")
	// The browser arrived here from the provider, not from the console. See
	// Other means the redirect is a GET regardless of what the provider's
	// transport was.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (c *ConsoleIdentityController) unconfigured(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{responseMessageField: identityUnconfigured})
}

// refuse answers a dance that did not complete, and clears any session
// cookies the browser carries for the same reason the other session routes
// do: a refusal with a stale cookie would leave the console claiming a
// session while every request behind it fails.
func (c *ConsoleIdentityController) refuse(w http.ResponseWriter, r *http.Request, cause error) {
	for _, cookie := range localauth.ClearedSessionCookies(requestIsSecure(r)) {
		http.SetCookie(w, cookie)
	}
	log.Warn().Err(cause).Msg("Refused an identity console session")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{responseMessageField: identityRefusal})
}

func (c *ConsoleIdentityController) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
