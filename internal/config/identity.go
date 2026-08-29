package config

// This file holds the identity settings: the OAuth acquisition path an
// operator turns on. A deployment that sets none of this has no identity
// surface at all — the grant stays inert and the gateway behaves exactly as
// it does today.

import (
	"github.com/agentstation/starport/internal/identity"
)

// IdentityConfig defines how people authenticate to the console through an
// external identity provider.
type IdentityConfig struct {
	OAuth OAuthIdentityConfig `env:",prefix=OAUTH_"`
}

// OAuthIdentityConfig names the OAuth applications an operator registered.
// CallbackBaseURL is the address a provider sends the browser back to —
// scheme and host, no path — and each provider is on when both halves of its
// application credential are set.
type OAuthIdentityConfig struct {
	CallbackBaseURL string                 `env:"CALLBACK_BASE_URL" redact:"url"`
	Google          OAuthApplicationConfig `env:",prefix=GOOGLE_"`
	GitHub          OAuthApplicationConfig `env:",prefix=GITHUB_"`
}

// OAuthApplicationConfig is one registered OAuth application's credential
// pair.
type OAuthApplicationConfig struct {
	ClientID     string `env:"CLIENT_ID"`
	ClientSecret string `env:"CLIENT_SECRET" secret:"true"`
}

// on reports whether the operator supplied this application at all.
func (c OAuthApplicationConfig) on() bool {
	return c.ClientID != "" || c.ClientSecret != ""
}

// Enabled reports whether any OAuth provider is configured.
func (c OAuthIdentityConfig) Enabled() bool {
	return c.Google.on() || c.GitHub.on()
}

// RuntimeOAuth projects the operator's settings into the identity contract.
// A half-configured provider is passed through rather than dropped, so the
// acquisition path can refuse it with a named error instead of this
// projection silently turning it off.
func (c IdentityConfig) RuntimeOAuth() identity.OAuthConfig {
	cfg := identity.OAuthConfig{CallbackBaseURL: c.OAuth.CallbackBaseURL}
	for name, application := range map[string]OAuthApplicationConfig{
		"google": c.OAuth.Google,
		"github": c.OAuth.GitHub,
	} {
		if application.on() {
			cfg.Providers = append(cfg.Providers, identity.OAuthProvider{
				Name:         name,
				ClientID:     application.ClientID,
				ClientSecret: application.ClientSecret,
			})
		}
	}
	return cfg
}
