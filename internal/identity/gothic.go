package identity

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
)

// This file is the gothic acquisition path: the OAuth dance against the
// per-provider applications an operator registered. It proves who a person is
// and hands the profile across the seam in authenticator.go, which owns
// everything after that proof.

// OAuthProvider is one operator-configured OAuth application. The name is the
// provider this gateway supports — see supportedOAuthProviders — and the pair
// is the application the operator registered with that provider.
type OAuthProvider struct {
	Name         string
	ClientID     string
	ClientSecret string
}

var (
	// ErrIncompleteOAuthProvider reports a provider missing its client ID or
	// secret.
	ErrIncompleteOAuthProvider = errors.New("OAuth provider needs a client ID and a client secret")
)

// supportedOAuthProviders maps a config name to the goth constructor for it.
// Growing this map is the whole cost of supporting another provider.
var supportedOAuthProviders = map[string]func(clientID, secret, callbackURL string) goth.Provider{
	"google": func(clientID, secret, callbackURL string) goth.Provider {
		return google.New(clientID, secret, callbackURL, "email", "profile")
	},
	"github": func(clientID, secret, callbackURL string) goth.Provider {
		return github.New(clientID, secret, callbackURL, "user:email")
	},
}

// gothicPath is the OAuth acquisition path. It wires gothic over the
// operator's configured providers and performs the dance; the seam above it
// owns dispatch, so it never sees a provider it did not register.
type gothicPath struct{}

// newGothicPath registers each configured provider with goth and returns the
// path beside the names it serves. It owns gothic's session store, so the
// composition root configures nothing about gothic directly.
func newGothicPath(base string, providers []OAuthProvider) (*gothicPath, []string, error) {
	registered := make([]goth.Provider, 0, len(providers))
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		construct, known := supportedOAuthProviders[provider.Name]
		if !known {
			return nil, nil, fmt.Errorf("%w: %s", ErrUnknownProvider, provider.Name)
		}
		if provider.ClientID == "" || provider.ClientSecret == "" {
			return nil, nil, fmt.Errorf("%w: %s", ErrIncompleteOAuthProvider, provider.Name)
		}
		registered = append(registered, construct(
			provider.ClientID, provider.ClientSecret, base+CallbackPath(provider.Name)))
		names = append(names, provider.Name)
	}
	goth.UseProviders(registered...)
	if err := ensureGothicStore(); err != nil {
		return nil, nil, err
	}
	return &gothicPath{}, names, nil
}

// ensureGothicStore gives gothic a cookie store keyed for this process. The
// store carries only the in-flight OAuth state — the CSRF token and the
// provider session — so a fresh random key per boot is correct: a restart
// mid-dance restarts the dance, nothing else.
var ensureGothicStore = sync.OnceValue(func() error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("key gothic's state store: %w", err)
	}
	store := sessions.NewCookieStore(key)
	store.Options.HttpOnly = true
	store.MaxAge(int(claimTTL.Seconds() * 10))
	gothic.Store = store
	return nil
})

// begin redirects the browser to the named provider's consent page.
func (g *gothicPath) begin(w http.ResponseWriter, r *http.Request, provider string) error {
	r = gothic.GetContextWithProvider(r, provider)
	url, err := gothic.GetAuthURL(w, r)
	if err != nil {
		return fmt.Errorf("begin OAuth with %s: %w", provider, err)
	}
	// #nosec G710 -- The destination is the registered provider's consent
	// URL, built by gothic from this deployment's own configuration; nothing
	// in the request chooses where the browser goes.
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	return nil
}

// complete finishes the provider's callback: it verifies the OAuth state,
// fetches the profile, and hands who the provider vouched for across the
// seam.
func (g *gothicPath) complete(
	w http.ResponseWriter, r *http.Request, provider string,
) (acquiredIdentity, error) {
	r = gothic.GetContextWithProvider(r, provider)
	profile, err := gothic.CompleteUserAuth(w, r)
	if err != nil {
		return acquiredIdentity{}, fmt.Errorf("complete OAuth with %s: %w", provider, err)
	}
	return acquiredIdentity{
		id:          profile.UserID,
		email:       profile.Email,
		displayName: profile.Name,
	}, nil
}
