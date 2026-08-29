package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
)

// TestIdentityEnabled pins when the identity seam exists at all: any half of
// any acquisition path turns it on, so a misconfiguration is refused loudly
// downstream instead of silently ignored here.
func TestIdentityEnabled(t *testing.T) {
	require.False(t, IdentityConfig{}.Enabled())
	require.True(t, IdentityConfig{OAuth: OAuthIdentityConfig{
		Google: OAuthApplicationConfig{ClientID: "id"},
	}}.Enabled())
	require.True(t, IdentityConfig{OAuth: OAuthIdentityConfig{
		GitHub: OAuthApplicationConfig{ClientSecret: "secret"},
	}}.Enabled())
	require.True(t, IdentityConfig{WorkOS: WorkOSIdentityConfig{
		APIKey: "sk_test",
	}}.Enabled())
	require.True(t, IdentityConfig{WorkOS: WorkOSIdentityConfig{
		Organization: "org_01H",
	}}.Enabled())
}

// TestRuntimeAcquisitionProjectsProviders holds the projection: every
// configured acquisition path crosses into the identity contract,
// half-configured ones included.
func TestRuntimeAcquisitionProjectsProviders(t *testing.T) {
	cfg := IdentityConfig{
		CallbackBaseURL: "https://gateway.example.com",
		OAuth: OAuthIdentityConfig{
			Google: OAuthApplicationConfig{ClientID: "gid", ClientSecret: "gsecret"},
			GitHub: OAuthApplicationConfig{ClientID: "hid"},
		},
		WorkOS: WorkOSIdentityConfig{
			APIKey: "sk_test", ClientID: "client_01", Organization: "org_01H",
		},
	}

	got := cfg.RuntimeAcquisition()
	require.Equal(t, "https://gateway.example.com", got.CallbackBaseURL)
	require.ElementsMatch(t, []identity.OAuthProvider{
		{Name: "google", ClientID: "gid", ClientSecret: "gsecret"},
		{Name: "github", ClientID: "hid"},
	}, got.OAuthProviders)
	require.Equal(t, identity.WorkOSConfig{
		APIKey: "sk_test", ClientID: "client_01", Organization: "org_01H",
	}, got.WorkOS)
}
