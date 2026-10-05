package providers

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/config"
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

func TestDeploymentApprovalsWithoutSettingsMatchInstallationDefaults(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	installation, err := InstallationDestinationApprovals(snapshot)
	require.NoError(t, err)
	for _, settings := range []config.ProvidersConfig{
		nil,
		{catalogs.ProviderIDOpenAI: {Enabled: true}},
		// An explicit base URL without the setting approves nothing.
		{catalogs.ProviderIDOpenAI: {BaseURL: "http://127.0.0.1:8089/relay"}},
	} {
		deployment, err := DeploymentDestinationApprovals(snapshot, settings)
		require.NoError(t, err)
		want, err := installation.RecoverySelectionSHA256()
		require.NoError(t, err)
		got, err := deployment.RecoverySelectionSHA256()
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestDeploymentApprovalsBindOperatorOriginToEnvironmentRoleOnly(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	approvals, err := DeploymentDestinationApprovals(snapshot, config.ProvidersConfig{
		catalogs.ProviderIDOpenAI: {BaseURL: "http://127.0.0.1:8089/relay", InferenceOrigin: "http://127.0.0.1:8089/relay/"},
	})
	require.NoError(t, err)
	provider, err := snapshot.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	material := credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{Handle: "fixture-handle"})
	const override = "http://127.0.0.1:8089/relay/v1/chat/completions"
	const catalogURL = "https://api.openai.com/v1/chat/completions"
	for _, test := range []struct {
		role    keyring.CredentialSource
		allowed string
		denied  string
	}{
		{role: keyring.SourceEnvironment, allowed: override, denied: catalogURL},
		{role: keyring.SourceShared, allowed: catalogURL, denied: override},
		{role: keyring.SourceBYOK, allowed: catalogURL, denied: override},
	} {
		t.Run(string(test.role), func(t *testing.T) {
			bound, err := approvals.Bind(provider.ID, string(test.role), material, catalogs.ProviderOperationChatCompletions)
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, test.allowed, nil)
			require.NoError(t, err)
			_, err = bound.AuthorizeDestination(request)
			require.NoError(t, err)
			request, err = http.NewRequestWithContext(t.Context(), http.MethodPost, test.denied, nil)
			require.NoError(t, err)
			_, err = bound.AuthorizeDestination(request)
			require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
		})
	}
}

func TestDeploymentApprovalsKeepPrivateAndParameterizedCatalogOriginsClosed(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	t.Run("private catalog origin gets only the environment override", func(t *testing.T) {
		provider, err := snapshot.Provider(catalogs.ProviderIDOpenAI)
		require.NoError(t, err)
		provider.Inference.BaseURL = "https://10.0.0.1"
		builder, err := catalogs.NewBuilderFrom(snapshot)
		require.NoError(t, err)
		require.NoError(t, builder.SetProvider(provider))
		private, err := builder.Build()
		require.NoError(t, err)
		approvals, err := DeploymentDestinationApprovals(private, config.ProvidersConfig{
			provider.ID: {BaseURL: "https://relay.example", InferenceOrigin: "https://relay.example"},
		})
		require.NoError(t, err)
		material := credentials.NewMaterial(provider.Credentials.Profiles[0], nil, credentials.MaterialMetadata{Handle: "fixture-handle"})
		_, err = approvals.Bind(provider.ID, string(keyring.SourceEnvironment), material, catalogs.ProviderOperationChatCompletions)
		require.NoError(t, err)
		for _, role := range []keyring.CredentialSource{keyring.SourceShared, keyring.SourceBYOK} {
			_, err = approvals.Bind(provider.ID, string(role), material, catalogs.ProviderOperationChatCompletions)
			require.ErrorIs(t, err, credentials.ErrDestinationUnapproved, role)
		}
	})
	t.Run("parameterized provider ignores a resolved base URL", func(t *testing.T) {
		installation, err := InstallationDestinationApprovals(snapshot)
		require.NoError(t, err)
		deployment, err := DeploymentDestinationApprovals(snapshot, config.ProvidersConfig{
			"ollama": {BaseURL: "http://127.0.0.1:11434", InferenceOrigin: "http://127.0.0.1:11434"},
		})
		require.NoError(t, err)
		want, err := installation.RecoverySelectionSHA256()
		require.NoError(t, err)
		got, err := deployment.RecoverySelectionSHA256()
		require.NoError(t, err)
		require.Equal(t, want, got)
	})
}
