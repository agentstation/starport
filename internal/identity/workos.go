package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	workos "github.com/workos/workos-go/v10"
)

// This file is the WorkOS acquisition path: enterprise SSO through the broker
// an organization already federates with, beside the per-provider OAuth
// applications gothic serves. It proves who a person is and hands the profile
// across the seam in authenticator.go, which owns everything after that
// proof — so an enterprise arrival and an OAuth arrival become the same user
// shape through the same code.

// workosProviderName is the one name this path serves. It is the segment in
// the identity routes and the prefix of every subject this path acquires.
const workosProviderName = "workos"

// workosStateCookie carries the CSRF state across the WorkOS dance, the same
// job gothic's session store does for the OAuth paths.
const workosStateCookie = "starport_workos_state"

var (
	// ErrIncompleteWorkOS reports a WorkOS config missing its credential
	// halves.
	ErrIncompleteWorkOS = errors.New("WorkOS needs an API key and a client ID")
	// ErrWorkOSDestinationRequired reports a WorkOS config that names no
	// organization or connection, so WorkOS would not know which enterprise's
	// people to ask for.
	ErrWorkOSDestinationRequired = errors.New("WorkOS needs an organization or a connection")
)

// WorkOSConfig is what an operator supplies to open the WorkOS acquisition
// path. APIKey and ClientID come from the WorkOS dashboard; Organization or
// Connection names which enterprise directory the dance goes to, and at least
// one is required.
type WorkOSConfig struct {
	APIKey       string
	ClientID     string
	Organization string
	Connection   string
	// Endpoint overrides the WorkOS API base URL. Tests point it at a stub;
	// production leaves it empty.
	Endpoint string
}

// configured reports whether the operator supplied any of this at all.
func (c WorkOSConfig) configured() bool {
	return c.APIKey != "" || c.ClientID != "" ||
		c.Organization != "" || c.Connection != ""
}

// workosPath is the WorkOS acquisition path. The seam above it owns dispatch,
// so it only ever sees the one provider name it registered.
type workosPath struct {
	client       *workos.Client
	redirectURI  string
	organization string
	connection   string
	// secure mirrors the callback base's scheme: an https deployment gets a
	// Secure state cookie, a local http one still works.
	secure bool
}

// newWorkOSPath validates the operator's WorkOS settings and opens the path.
func newWorkOSPath(base string, cfg WorkOSConfig) (*workosPath, error) {
	if cfg.APIKey == "" || cfg.ClientID == "" {
		return nil, ErrIncompleteWorkOS
	}
	if cfg.Organization == "" && cfg.Connection == "" {
		return nil, ErrWorkOSDestinationRequired
	}
	options := []workos.ClientOption{workos.WithClientID(cfg.ClientID)}
	if cfg.Endpoint != "" {
		options = append(options, workos.WithBaseURL(cfg.Endpoint))
	}
	return &workosPath{
		client:       workos.NewClient(cfg.APIKey, options...),
		redirectURI:  base + CallbackPath(workosProviderName),
		organization: cfg.Organization,
		connection:   cfg.Connection,
		secure:       strings.HasPrefix(base, "https://"),
	}, nil
}

// begin sends the browser to WorkOS, which routes the person to the
// organization's own identity provider. The state cookie is this path's CSRF
// guard: the callback must carry back exactly what begin issued.
func (p *workosPath) begin(w http.ResponseWriter, r *http.Request, _ string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("issue WorkOS state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	// #nosec G124 -- the rule wants Secure unconditionally. It mirrors the
	// operator's callback scheme instead: a local http deployment could never
	// finish the dance with a Secure-only cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     workosStateCookie,
		Value:    state,
		Path:     "/console/identity/" + workosProviderName,
		MaxAge:   int(claimTTL.Seconds() * 10),
		Secure:   p.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	params := &workos.SSOGetAuthorizationURLParams{
		RedirectURI: p.redirectURI,
		State:       workos.String(state),
	}
	if p.organization != "" {
		params.Organization = workos.String(p.organization)
	}
	if p.connection != "" {
		params.Connection = workos.String(p.connection)
	}
	// #nosec G710 -- The destination is WorkOS's authorize URL, built by the
	// SDK from this deployment's own configuration; nothing in the request
	// chooses where the browser goes.
	http.Redirect(w, r, p.client.SSO().GetAuthorizationURL(params), http.StatusTemporaryRedirect)
	return nil
}

// complete finishes the WorkOS callback: it verifies the state, exchanges the
// code for the brokered profile, and hands who the enterprise vouched for
// across the seam.
func (p *workosPath) complete(
	w http.ResponseWriter, r *http.Request, _ string,
) (acquiredIdentity, error) {
	cookie, err := r.Cookie(workosStateCookie)
	if err != nil || cookie.Value == "" {
		return acquiredIdentity{}, errors.New("WorkOS callback carries no state cookie")
	}
	// The dance ends here either way; the state is single-use.
	// #nosec G124 -- the rule reads the attributes of a cookie being expired.
	http.SetCookie(w, &http.Cookie{
		Name:     workosStateCookie,
		Path:     "/console/identity/" + workosProviderName,
		MaxAge:   -1,
		Secure:   p.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	state := r.URL.Query().Get("state")
	if subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
		return acquiredIdentity{}, errors.New("WorkOS callback state does not match the one begin issued")
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		return acquiredIdentity{}, errors.New("WorkOS callback carries no code")
	}
	token, err := p.client.SSO().GetProfileAndToken(
		r.Context(), &workos.SSOGetProfileAndTokenParams{Code: code})
	if err != nil {
		return acquiredIdentity{}, fmt.Errorf("exchange WorkOS code: %w", err)
	}
	if token.Profile == nil {
		return acquiredIdentity{}, errors.New("WorkOS returned no profile for this person")
	}
	return acquiredIdentity{
		id:          token.Profile.ID,
		email:       token.Profile.Email,
		displayName: workosDisplayName(token.Profile),
	}, nil
}

// workosDisplayName folds the profile's name fields into the one display name
// the user model carries: the full name when the directory sent one, the
// joined parts otherwise.
func workosDisplayName(profile *workos.Profile) string {
	if profile.Name != nil && strings.TrimSpace(*profile.Name) != "" {
		return strings.TrimSpace(*profile.Name)
	}
	parts := ""
	if profile.FirstName != nil {
		parts = *profile.FirstName
	}
	if profile.LastName != nil {
		parts += " " + *profile.LastName
	}
	return strings.Join(strings.Fields(parts), " ")
}
