package config

// This file holds the identity settings: the acquisition paths an operator
// turns on — per-provider OAuth applications, enterprise SSO through WorkOS,
// either or both. A deployment that sets none of this has no identity
// surface at all — the grant stays inert and the gateway behaves exactly as
// it does today.

import (
	"github.com/agentstation/starport/internal/identity"
)

// IdentityConfig defines how people authenticate to the console through an
// external identity provider. CallbackBaseURL is the address every provider
// sends the browser back to — scheme and host, no path — and is shared by
// every acquisition path.
type IdentityConfig struct {
	CallbackBaseURL string               `env:"CALLBACK_BASE_URL" redact:"url"`
	OAuth           OAuthIdentityConfig  `env:",prefix=OAUTH_"`
	WorkOS          WorkOSIdentityConfig `env:",prefix=WORKOS_"`
}

// OAuthIdentityConfig names the OAuth applications an operator registered.
// Each provider is on when both halves of its application credential are
// set.
type OAuthIdentityConfig struct {
	Google OAuthApplicationConfig `env:",prefix=GOOGLE_"`
	GitHub OAuthApplicationConfig `env:",prefix=GITHUB_"`
}

// OAuthApplicationConfig is one registered OAuth application's credential
// pair.
type OAuthApplicationConfig struct {
	ClientID     string `env:"CLIENT_ID"`
	ClientSecret string `env:"CLIENT_SECRET" secret:"true"`
}

// WorkOSIdentityConfig is the enterprise SSO broker's settings. APIKey and
// ClientID come from the WorkOS dashboard; Organization or Connection names
// which enterprise directory people arrive from.
type WorkOSIdentityConfig struct {
	APIKey       string `env:"API_KEY" secret:"true"`
	ClientID     string `env:"CLIENT_ID"`
	Organization string `env:"ORGANIZATION"`
	Connection   string `env:"CONNECTION"`
}

// on reports whether the operator supplied this application at all.
func (c OAuthApplicationConfig) on() bool {
	return c.ClientID != "" || c.ClientSecret != ""
}

// on reports whether the operator supplied any WorkOS setting at all.
func (c WorkOSIdentityConfig) on() bool {
	return c.APIKey != "" || c.ClientID != "" || c.Organization != "" || c.Connection != ""
}

// Enabled reports whether any OAuth provider is configured.
func (c OAuthIdentityConfig) Enabled() bool {
	return c.Google.on() || c.GitHub.on()
}

// Enabled reports whether any acquisition path is configured.
func (c IdentityConfig) Enabled() bool {
	return c.OAuth.Enabled() || c.WorkOS.on()
}

// RuntimeAcquisition projects the operator's settings into the identity
// contract. A half-configured path is passed through rather than dropped, so
// the acquisition path can refuse it with a named error instead of this
// projection silently turning it off.
func (c IdentityConfig) RuntimeAcquisition() identity.AcquisitionConfig {
	cfg := identity.AcquisitionConfig{
		CallbackBaseURL: c.CallbackBaseURL,
		WorkOS: identity.WorkOSConfig{
			APIKey:       c.WorkOS.APIKey,
			ClientID:     c.WorkOS.ClientID,
			Organization: c.WorkOS.Organization,
			Connection:   c.WorkOS.Connection,
		},
	}
	for name, application := range map[string]OAuthApplicationConfig{
		"google": c.OAuth.Google,
		"github": c.OAuth.GitHub,
	} {
		if application.on() {
			cfg.OAuthProviders = append(cfg.OAuthProviders, identity.OAuthProvider{
				Name:         name,
				ClientID:     application.ClientID,
				ClientSecret: application.ClientSecret,
			})
		}
	}
	return cfg
}
