package identity

import (
	"testing"

	"github.com/markbates/goth"
	"github.com/stretchr/testify/require"
)

func TestAcquisitionValidationDoesNotRegisterOrReachProviders(t *testing.T) {
	before := len(goth.GetProviders())
	cfg := AcquisitionConfig{CallbackBaseURL: "http://localhost:7827", OAuthProviders: []OAuthProvider{{Name: "google", ClientID: "fixture-id", ClientSecret: "fixture-secret"}}, WorkOS: WorkOSConfig{APIKey: "fixture", ClientID: "fixture", Organization: "fixture", Endpoint: "https://unreachable.invalid"}}
	require.NoError(t, cfg.Validate())
	require.Equal(t, before, len(goth.GetProviders()))
	cfg.OAuthProviders[0].ClientSecret = ""
	require.ErrorIs(t, cfg.Validate(), ErrIncompleteOAuthProvider)
	require.Equal(t, before, len(goth.GetProviders()))
	cfg.OAuthProviders = nil
	cfg.WorkOS.Organization = ""
	require.ErrorIs(t, cfg.Validate(), ErrWorkOSDestinationRequired)
}
