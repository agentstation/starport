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
)

// This file is the one identity seam. Every acquisition path — gothic OAuth,
// WorkOS SSO — performs its own dance and hands back who the provider vouched
// for; everything after that point is shared and lives here: the subject, the
// user model resolution, the one-time claim, and the redemption the identity
// grant calls. A new way of proving who someone is becomes a new path behind
// this seam, never a second seam.

var (
	// ErrNoProvidersConfigured reports an acquisition config with nothing in
	// it. The caller should not have built an acquisition path at all.
	ErrNoProvidersConfigured = errors.New("no identity providers configured")
	// ErrCallbackBaseRequired reports a config that names providers but no
	// address for them to send the browser back to.
	ErrCallbackBaseRequired = errors.New("identity providers need a callback base URL")
	// ErrUnknownProvider reports a provider name no configured acquisition
	// path serves.
	ErrUnknownProvider = errors.New("unknown identity provider")
	// ErrClaimInvalid reports an identity claim that is unknown, spent, or
	// expired. One message for all three: a claim is redeemed once, seconds
	// after it is issued, and which way it failed tells a caller nothing
	// actionable.
	ErrClaimInvalid = errors.New("identity claim is invalid or expired")
)

// AcquisitionConfig is what an operator supplies to open the identity seam.
// CallbackBaseURL is the address every provider sends the browser back to,
// scheme and host only; the per-provider callback path is appended so the
// operator registers exactly the URL this gateway serves. Each acquisition
// half is on when it names anything at all, and a half-configured one is
// refused with a named error instead of silently dropped.
type AcquisitionConfig struct {
	CallbackBaseURL string
	OAuthProviders  []OAuthProvider
	WorkOS          WorkOSConfig
}

// CallbackPath returns the route a provider redirects back to. It lives here
// so the URL an operator registers and the route the server mounts cannot
// drift apart.
func CallbackPath(provider string) string {
	return "/console/identity/" + provider + "/callback"
}

// claimTTL bounds how long an issued identity claim stays redeemable. The
// claim crosses one in-process handoff — callback handler to identity grant —
// so a minute is generous and anything older is a claim that leaked.
const claimTTL = time.Minute

// acquiredIdentity is what a completed dance hands across the seam: the
// provider's stable identifier for the person and the profile fields that
// describe them. The path proves who the person is; the seam decides what
// that proof becomes.
type acquiredIdentity struct {
	id          string
	email       string
	displayName string
}

// acquisitionPath is one way a person proves who they are. Begin sends the
// browser to the provider; complete verifies what came back. Dispatch by
// provider name happens above this contract, so a path only ever sees names
// it registered.
type acquisitionPath interface {
	begin(w http.ResponseWriter, r *http.Request, provider string) error
	complete(w http.ResponseWriter, r *http.Request, provider string) (acquiredIdentity, error)
}

// Authenticator is the identity seam's one value: it dispatches each request
// to the acquisition path serving that provider, resolves every completed
// dance to the one user model, and answers the identity grant's Authenticate
// with the subject a redeemed claim names.
//
// It implements localauth.IdentityProvider structurally: this package stays
// below the grant seam in the import graph, and the composition root hands
// the value across.
type Authenticator struct {
	users  UserRepository
	paths  map[string]acquisitionPath
	names  []string
	claims claimBroker
	// now is injected for tests. A nil value means time.Now.
	now func() time.Time
}

// NewAuthenticator opens the identity seam over the operator's configured
// acquisition paths: gothic for the OAuth providers, WorkOS for enterprise
// SSO, either or both.
func NewAuthenticator(cfg AcquisitionConfig, users UserRepository) (*Authenticator, error) {
	if len(cfg.OAuthProviders) == 0 && !cfg.WorkOS.configured() {
		return nil, ErrNoProvidersConfigured
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.CallbackBaseURL), "/")
	if base == "" {
		return nil, ErrCallbackBaseRequired
	}
	paths := map[string]acquisitionPath{}
	if len(cfg.OAuthProviders) > 0 {
		oauth, names, err := newGothicPath(base, cfg.OAuthProviders)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			paths[name] = oauth
		}
	}
	if cfg.WorkOS.configured() {
		sso, err := newWorkOSPath(base, cfg.WorkOS)
		if err != nil {
			return nil, err
		}
		paths[workosProviderName] = sso
	}
	return newAuthenticator(users, paths)
}

// newAuthenticator is the constructor under NewAuthenticator, split so a test
// can wire its own acquisition path and skip the real constructors.
func newAuthenticator(users UserRepository, paths map[string]acquisitionPath) (*Authenticator, error) {
	if users == nil {
		return nil, ErrRepositoryRequired
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	return &Authenticator{
		users:  users,
		paths:  paths,
		names:  names,
		claims: claimBroker{entries: map[string]claimEntry{}},
	}, nil
}

// Providers reports the configured provider names, sorted. The console reads
// this to know which buttons to draw.
func (a *Authenticator) Providers() []string {
	names := make([]string, len(a.names))
	copy(names, a.names)
	return names
}

// path resolves a provider name to the acquisition path the operator turned
// on for it. A provider some path could serve but this deployment did not
// configure is unknown here on purpose.
func (a *Authenticator) path(provider string) (acquisitionPath, error) {
	found, configured := a.paths[provider]
	if !configured {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
	}
	return found, nil
}

// Begin redirects the browser to the named provider's consent page.
func (a *Authenticator) Begin(w http.ResponseWriter, r *http.Request, provider string) error {
	acquisition, err := a.path(provider)
	if err != nil {
		return err
	}
	return acquisition.begin(w, r, provider)
}

// Complete finishes the provider's callback: the acquisition path verifies
// what came back, the seam resolves the person to the one user model, and the
// caller receives a one-time claim the identity grant will redeem. The claim
// is the seam: a path proved who the person is, and only the grant may turn
// that proof into a session.
func (a *Authenticator) Complete(w http.ResponseWriter, r *http.Request, provider string) (string, error) {
	acquisition, err := a.path(provider)
	if err != nil {
		return "", err
	}
	acquired, err := acquisition.complete(w, r, provider)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(acquired.id) == "" {
		return "", fmt.Errorf("%s named no subject for this person", provider)
	}
	subject := provider + ":" + acquired.id
	if err := a.resolveUser(r.Context(), subject, acquired.email, acquired.displayName); err != nil {
		return "", err
	}
	return a.claims.issue(subject, a.clock()), nil
}

// Authenticate redeems a claim for the subject it names. It is the
// localauth.IdentityProvider contract: the grant calls it once per claim,
// and a second redemption is a replay.
func (a *Authenticator) Authenticate(claim string) (string, error) {
	return a.claims.redeem(claim, a.clock())
}

// resolveUser folds one authenticated profile into the user model: the first
// arrival of a subject creates the user, and every later arrival refreshes
// the profile fields. The subject is the identity; email and display name
// are descriptions of it.
func (a *Authenticator) resolveUser(ctx context.Context, subject, email, displayName string) error {
	now := a.clock().UTC()
	record, err := a.users.GetBySubject(ctx, subject)
	if errors.Is(err, ErrUserNotFound) {
		user := User{
			ID:          uuid.NewString(),
			Subject:     subject,
			Email:       email,
			DisplayName: displayName,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		_, err = a.users.Create(ctx, user)
		if errors.Is(err, ErrUserConflict) {
			// Two callbacks for the same new person raced; the other one won.
			record, err = a.users.GetBySubject(ctx, subject)
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
	_, err = a.users.Update(ctx, updated, record.Revision)
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

func (a *Authenticator) clock() time.Time {
	if a.now != nil {
		return a.now()
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
