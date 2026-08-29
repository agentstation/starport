package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
)

// TestIdentityOAuthEnabled pins when the acquisition path exists at all: any
// half of any application credential turns it on, so a misconfiguration is
// refused loudly downstream instead of silently ignored here.
func TestIdentityOAuthEnabled(t *testing.T) {
	require.False(t, OAuthIdentityConfig{}.Enabled())
	require.True(t, OAuthIdentityConfig{
		Google: OAuthApplicationConfig{ClientID: "id"},
	}.Enabled())
	require.True(t, OAuthIdentityConfig{
		GitHub: OAuthApplicationConfig{ClientSecret: "secret"},
	}.Enabled())
}

// TestRuntimeOAuthProjectsProviders holds the projection: every configured
// application crosses into the identity contract under its provider name,
// half-configured ones included.
func TestRuntimeOAuthProjectsProviders(t *testing.T) {
	cfg := IdentityConfig{OAuth: OAuthIdentityConfig{
		CallbackBaseURL: "https://gateway.example.com",
		Google:          OAuthApplicationConfig{ClientID: "gid", ClientSecret: "gsecret"},
		GitHub:          OAuthApplicationConfig{ClientID: "hid"},
	}}

	got := cfg.RuntimeOAuth()
	require.Equal(t, "https://gateway.example.com", got.CallbackBaseURL)
	require.ElementsMatch(t, []identity.OAuthProvider{
		{Name: "google", ClientID: "gid", ClientSecret: "gsecret"},
		{Name: "github", ClientID: "hid"},
	}, got.Providers)
}
