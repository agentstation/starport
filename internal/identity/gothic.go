package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
)

// OAuthProvider is one operator-configured OAuth application. The name is the
// provider this gateway supports — see supportedOAuthProviders — and the pair
// is the application the operator registered with that provider.
type OAuthProvider struct {
	Name         string
	ClientID     string
	ClientSecret string
}

// OAuthConfig is what an operator supplies to open the OAuth acquisition
// path. CallbackBaseURL is the address a provider sends the browser back to,
// scheme and host only; the per-provider callback path is appended here so
// the operator registers exactly the URL this gateway serves.
type OAuthConfig struct {
	CallbackBaseURL string
	Providers       []OAuthProvider
}

// CallbackPath returns the route a provider redirects back to. It lives here
// so the URL an operator registers and the route the server mounts cannot
// drift apart.
func CallbackPath(provider string) string {
	return "/console/identity/" + provider + "/callback"
}

var (
	// ErrNoProvidersConfigured reports an OAuth config with no providers. The
	// caller should not have built an acquisition path at all.
	ErrNoProvidersConfigured = errors.New("no OAuth providers configured")
	// ErrCallbackBaseRequired reports a config that names providers but no
	// address for them to send the browser back to.
	ErrCallbackBaseRequired = errors.New("OAuth providers need a callback base URL")
	// ErrUnknownOAuthProvider reports a provider name this gateway does not
	// carry a constructor for.
	ErrUnknownOAuthProvider = errors.New("unknown OAuth provider")
	// ErrIncompleteOAuthProvider reports a provider missing its client ID or
	// secret.
	ErrIncompleteOAuthProvider = errors.New("OAuth provider needs a client ID and a client secret")
	// ErrClaimInvalid reports an identity claim that is unknown, spent, or
	// expired. One message for all three: a claim is redeemed once, seconds
	// after it is issued, and which way it failed tells a caller nothing
	// actionable.
	ErrClaimInvalid = errors.New("identity claim is invalid or expired")
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

// claimTTL bounds how long an issued identity claim stays redeemable. The
// claim crosses one in-process handoff — callback handler to identity grant —
// so a minute is generous and anything older is a claim that leaked.
const claimTTL = time.Minute

// Gothic is the OAuth acquisition path. It wires gothic over the operator's
// configured providers, resolves each completed callback to the one user
// model, and answers the identity grant's Authenticate with the subject a
// redeemed claim names.
//
// It implements localauth.IdentityProvider structurally: this package stays
// below the grant seam in the import graph, and the composition root hands
// the value across.
type Gothic struct {
	users     UserRepository
	providers []string
	claims    claimBroker
	// now is injected for tests. A nil value means time.Now.
	now func() time.Time
}

// NewGothic opens the OAuth acquisition path over the operator's providers.
// It registers each provider with goth and owns gothic's session store, so
// the composition root configures nothing about gothic directly.
func NewGothic(cfg OAuthConfig, users UserRepository) (*Gothic, error) {
	if len(cfg.Providers) == 0 {
		return nil, ErrNoProvidersConfigured
	}
	if strings.TrimSpace(cfg.CallbackBaseURL) == "" {
		return nil, ErrCallbackBaseRequired
	}
	base := strings.TrimRight(cfg.CallbackBaseURL, "/")
	registered := make([]goth.Provider, 0, len(cfg.Providers))
	names := make([]string, 0, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		construct, known := supportedOAuthProviders[provider.Name]
		if !known {
			return nil, fmt.Errorf("%w: %s", ErrUnknownOAuthProvider, provider.Name)
		}
		if provider.ClientID == "" || provider.ClientSecret == "" {
			return nil, fmt.Errorf("%w: %s", ErrIncompleteOAuthProvider, provider.Name)
		}
		registered = append(registered, construct(
			provider.ClientID, provider.ClientSecret, base+CallbackPath(provider.Name)))
		names = append(names, provider.Name)
	}
	goth.UseProviders(registered...)
	return newGothic(users, names)
}

// newGothic is the constructor under NewGothic, split so a test can register
// its own goth provider and skip the real constructors.
func newGothic(users UserRepository, names []string) (*Gothic, error) {
	if users == nil {
		return nil, ErrRepositoryRequired
	}
	if err := ensureGothicStore(); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return &Gothic{
		users:     users,
		providers: names,
		claims:    claimBroker{entries: map[string]claimEntry{}},
	}, nil
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

// Providers reports the configured provider names, sorted. The console reads
// this to know which buttons to draw.
func (g *Gothic) Providers() []string {
	names := make([]string, len(g.providers))
	copy(names, g.providers)
	return names
}

// serves reports whether this path was configured for the named provider. A
// provider registered with goth by anything else in the process is not one
// the operator turned on.
func (g *Gothic) serves(provider string) bool {
	for _, name := range g.providers {
		if name == provider {
			return true
		}
	}
	return false
}

// Begin redirects the browser to the named provider's consent page.
func (g *Gothic) Begin(w http.ResponseWriter, r *http.Request, provider string) error {
	if !g.serves(provider) {
		return fmt.Errorf("%w: %s", ErrUnknownOAuthProvider, provider)
	}
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

// Complete finishes the provider's callback: it verifies the OAuth state,
// fetches the profile, resolves it to the one user model, and returns a
// one-time claim the identity grant will redeem. The claim is the seam:
// this package proved who the person is, and only the grant may turn that
// proof into a session.
func (g *Gothic) Complete(w http.ResponseWriter, r *http.Request, provider string) (string, error) {
	if !g.serves(provider) {
		return "", fmt.Errorf("%w: %s", ErrUnknownOAuthProvider, provider)
	}
	r = gothic.GetContextWithProvider(r, provider)
	profile, err := gothic.CompleteUserAuth(w, r)
	if err != nil {
		return "", fmt.Errorf("complete OAuth with %s: %w", provider, err)
	}
	if strings.TrimSpace(profile.UserID) == "" {
		return "", fmt.Errorf("%s named no subject for this person", provider)
	}
	subject := provider + ":" + profile.UserID
	if err := g.resolveUser(r.Context(), subject, profile.Email, profile.Name); err != nil {
		return "", err
	}
	return g.claims.issue(subject, g.clock()), nil
}

// Authenticate redeems a claim for the subject it names. It is the
// localauth.IdentityProvider contract: the grant calls it once per claim,
// and a second redemption is a replay.
func (g *Gothic) Authenticate(claim string) (string, error) {
	return g.claims.redeem(claim, g.clock())
}

// resolveUser folds one authenticated profile into the user model: the first
// arrival of a subject creates the user, and every later arrival refreshes
// the profile fields. The subject is the identity; email and display name
// are descriptions of it.
func (g *Gothic) resolveUser(ctx context.Context, subject, email, displayName string) error {
	now := g.clock().UTC()
	record, err := g.users.GetBySubject(ctx, subject)
	if errors.Is(err, ErrUserNotFound) {
		user := User{
			ID:          uuid.NewString(),
			Subject:     subject,
			Email:       email,
			DisplayName: displayName,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		_, err = g.users.Create(ctx, user)
		if errors.Is(err, ErrUserConflict) {
			// Two callbacks for the same new person raced; the other one won.
			record, err = g.users.GetBySubject(ctx, subject)
			if err != nil {
				return fmt.Errorf("resolve %s after a creation race: %w", subject, err)
			}
		} else if err != nil {
			return fmt.Errorf("create user for %s: %w", subject, err)
		} else {
			return nil
		}
	} else if err != nil {
		return fmt.Errorf("resolve %s: %w", subject, err)
	}
	if record.User.Email == email && record.User.DisplayName == displayName {
		return nil
	}
	updated := record.User
	updated.Email = email
	updated.DisplayName = displayName
	updated.UpdatedAt = now
	_, err = g.users.Update(ctx, updated, record.Revision)
	if errors.Is(err, ErrUserConflict) {
		// A concurrent arrival refreshed the profile first. The person is
		// resolved either way, which is all a callback needs.
		return nil
	}
	if err != nil {
		return fmt.Errorf("refresh profile for %s: %w", subject, err)
	}
	return nil
}

func (g *Gothic) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// claimBroker holds the one-time claims that cross from a completed callback
// to the identity grant. In-memory on purpose: a claim lives for seconds
// inside one process, so durable storage would only widen where it exists.
type claimBroker struct {
	mu      sync.Mutex
	entries map[string]claimEntry
}

type claimEntry struct {
	subject string
	expires time.Time
}

func (b *claimBroker) issue(subject string, now time.Time) string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// An exhausted entropy source is a broken host. A claim that cannot
		// be random must not exist.
		panic(fmt.Sprintf("issue identity claim: %v", err))
	}
	claim := base64.RawURLEncoding.EncodeToString(raw)
	b.mu.Lock()
	defer b.mu.Unlock()
	for value, entry := range b.entries {
		if now.After(entry.expires) {
			delete(b.entries, value)
		}
	}
	b.entries[claim] = claimEntry{subject: subject, expires: now.Add(claimTTL)}
	return claim
}

func (b *claimBroker) redeem(claim string, now time.Time) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, found := b.entries[claim]
	if !found {
		return "", ErrClaimInvalid
	}
	delete(b.entries, claim)
	if now.After(entry.expires) {
		return "", ErrClaimInvalid
	}
	return entry.subject, nil
}
