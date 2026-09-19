package providers

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/stretchr/testify/require"
)

func TestInstallationApprovalsBindCredentialRolesToBundledOrigin(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	approvals, err := InstallationDestinationApprovals(snapshot)
	require.NoError(t, err)
	provider, err := snapshot.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	material := credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{Handle: "fixture-handle"})
	for _, role := range []keyring.CredentialSource{keyring.SourceEnvironment, keyring.SourceShared, keyring.SourceBYOK} {
		bound, err := approvals.Bind(provider.ID, string(role), material, catalogs.ProviderOperationChatCompletions)
		require.NoError(t, err)
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.openai.com/v1/chat/completions", nil)
		require.NoError(t, err)
		_, err = bound.AuthorizeDestination(request)
		require.NoError(t, err)
		request.URL.Host = "other.example"
		request.Host = request.URL.Host
		_, err = bound.AuthorizeDestination(request)
		require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	}
}

func TestInstallationDefaultsRequireExplicitPrivateOriginApproval(t *testing.T) {
	withUserInfo := url.URL{Scheme: "https", Host: "provider.example", User: url.UserPassword("fixture-user", "fixture-password")}
	for _, origin := range []string{"http://provider.example", "https://127.0.0.1", "https://10.0.0.1", "https://[::1]", "https://[::ffff:127.0.0.1]", "https://localhost.", "https://service.localhost", "https://{location}.example", withUserInfo.String()} {
		require.False(t, publicInstallationOrigin(origin), origin)
	}
	require.True(t, publicInstallationOrigin("https://provider.example"))
	require.True(t, publicInstallationOrigin("https://8.8.8.8"))
}
