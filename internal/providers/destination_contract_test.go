package providers

import (
	starmap "github.com/agentstation/starmap"
	"net/http"
	"sync"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/stretchr/testify/require"
)

func TestApprovedCatalogContractDoesNotFollowProviderRefresh(t *testing.T) {
	profile := catalogs.ProviderCredentialProfile{ID: "key", Primitive: catalogs.ProviderAuthenticationAPIKey, Fields: []catalogs.ProviderCredentialFieldID{"key"}, Placements: []catalogs.ProviderCredentialPlacement{{Field: "key", Kind: catalogs.ProviderCredentialPlacementHeader, Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer}}}
	provider := catalogs.Provider{ID: "fixture", Credentials: &catalogs.ProviderCredentials{Profiles: []catalogs.ProviderCredentialProfile{profile}, Inference: catalogs.ProviderCredentialPlane{Alternatives: []catalogs.ProviderCredentialProfileID{profile.ID}}}, Inference: &catalogs.ProviderInference{BaseURL: "https://provider.example/v1", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeGoogle, Path: "/{provider_model_id}:generateContent", StreamPath: "/{provider_model_id}:streamGenerateContent"}}}}
	identity := credentials.DestinationIdentity{Provider: provider.ID, Role: string(keyring.SourceEnvironment), Handle: "fixture-handle"}
	grant, err := CompileDestinationGrant(provider, identity, profile.ID, "", nil)
	require.NoError(t, err)
	material := credentials.NewMaterial(profile, map[catalogs.ProviderCredentialFieldID]string{"key": "fixture-secret"}, credentials.MaterialMetadata{Handle: identity.Handle})
	for _, suffix := range []string{":generateContent", ":streamGenerateContent"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://provider.example/v1/models/new-model"+suffix, nil)
		require.NoError(t, err)
		_, err = grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, req)
		require.NoError(t, err)
		req.URL.Host = "changed.example"
		req.Host = req.URL.Host
		_, err = grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, req)
		require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	}
	provider.Inference.BaseURL = "https://changed.example/v1"
	provider.Credentials.Profiles[0].Placements[0].Name = "X-Changed-Key"
	changed := credentials.NewMaterial(provider.Credentials.Profiles[0], map[catalogs.ProviderCredentialFieldID]string{"key": "fixture-secret"}, credentials.MaterialMetadata{Handle: identity.Handle})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://changed.example/v1/models/new-model:generateContent", nil)
	require.NoError(t, err)
	_, err = grant.Authorize(identity, changed, catalogs.ProviderOperationChatCompletions, req)
	require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	replacement, err := CompileDestinationGrant(provider, identity, profile.ID, "", nil)
	require.NoError(t, err)
	_, err = replacement.Authorize(identity, changed, catalogs.ProviderOperationChatCompletions, req)
	require.NoError(t, err)
}

func TestEmbeddedDestinationContractsCompileWithoutProviderRoster(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	tested := 0
	for _, provider := range snapshot.Providers().List() {
		if provider.Inference == nil || provider.Credentials == nil {
			continue
		}
		for _, profile := range provider.Credentials.Profiles {
			if len(profile.EndpointBindings) != 0 {
				continue
			}
			selected := false
			for _, id := range provider.Credentials.Inference.Alternatives {
				selected = selected || id == profile.ID
			}
			if !selected {
				continue
			}
			t.Run(string(provider.ID)+"/"+string(profile.ID), func(t *testing.T) {
				identity := credentials.DestinationIdentity{Provider: provider.ID, Role: string(keyring.SourceEnvironment), Handle: "fixture-handle"}
				_, err := CompileDestinationGrant(provider, identity, profile.ID, "", nil)
				require.NoError(t, err)
			})
			tested++
		}
	}
	require.Positive(t, tested)
	t.Logf("compiled %d embedded inference profiles without endpoint parameters", tested)
}

func TestDestinationContractRequiresInferenceProfile(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	provider, err := snapshot.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	identity := credentials.DestinationIdentity{Provider: provider.ID, Role: string(keyring.SourceEnvironment), Handle: "fixture-handle"}
	id := provider.Credentials.Inference.Alternatives[0]
	provider.Credentials.Inference.Alternatives = nil
	_, err = CompileDestinationGrant(provider, identity, id, "", nil)
	require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
}

func TestDestinationContractScopesExplicitLocalOverride(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	provider, err := snapshot.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	profile := provider.Credentials.Profiles[0]
	identity := credentials.DestinationIdentity{Provider: provider.ID, Role: string(keyring.SourceBYOK), Handle: "fixture-account"}
	material := credentials.NewMaterial(profile, nil, credentials.MaterialMetadata{Handle: identity.Handle})
	grant, err := CompileDestinationGrant(provider, identity, profile.ID, "http://127.0.0.1:8123", nil)
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8123/v1/chat/completions", nil)
	require.NoError(t, err)
	_, err = grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
	require.NoError(t, err)
	for _, host := range []string{"127.0.0.1:8124", "other.example:8123"} {
		changed := request.Clone(t.Context())
		changed.URL.Host = host
		changed.Host = host
		_, err = grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, changed)
		require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	}
}

func TestDestinationContractRequiresFixedTenantParameters(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	provider, err := snapshot.Provider(catalogs.ProviderIDGoogleVertex)
	require.NoError(t, err)
	profile := provider.Credentials.Profiles[0]
	identity := credentials.DestinationIdentity{Provider: provider.ID, Role: string(keyring.SourceEnvironment), Handle: "fixture-handle"}
	_, err = CompileDestinationGrant(provider, identity, profile.ID, "", nil)
	require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	bindings := map[string]string{"project": "approved-project", "location": "us-central1"}
	grant, err := CompileDestinationGrant(provider, identity, profile.ID, "", bindings)
	require.NoError(t, err)
	material := credentials.NewMaterial(profile, nil, credentials.MaterialMetadata{Handle: identity.Handle})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://us-central1-aiplatform.googleapis.com/v1/projects/approved-project/locations/us-central1/publishers/google/models/new-model:generateContent", nil)
	require.NoError(t, err)
	_, err = grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
	require.NoError(t, err)
	request.URL.Path = "/v1/projects/other-project/locations/us-central1/publishers/google/models/new-model:generateContent"
	_, err = grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
	require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	for _, unsafe := range []map[string]string{{"project": "{wildcard}", "location": "us-central1"}, {"provider_model_id": "anything"}, {"unknown": "value"}} {
		_, err = CompileDestinationGrant(provider, identity, profile.ID, "", unsafe)
		require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	}
}

// The catalog is immutable. Provider reads return independent values for each test.
var destinationContractBaseline = sync.OnceValues(func() (*catalogs.Catalog, error) {
	builder, err := starmap.EmbeddedBuilder()
	if err != nil {
		return nil, err
	}
	return builder.Build()
})

func TestDestinationContractScopesJobTargets(t *testing.T) {
	snapshot, err := destinationContractBaseline()
	require.NoError(t, err)
	provider, err := snapshot.Provider(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	profile := provider.Credentials.Profiles[0]
	identity := credentials.DestinationIdentity{Provider: provider.ID, Role: string(keyring.SourceShared), Handle: "shared-fixture"}
	provider.Inference.Endpoints = []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationVideosGenerations, Type: catalogs.EndpointTypeOpenAI, Path: "/videos?version=1"}}
	grant, err := CompileDestinationGrant(provider, identity, profile.ID, "https://provider.example/v1", nil)
	require.NoError(t, err)
	material := credentials.NewMaterial(profile, nil, credentials.MaterialMetadata{Handle: identity.Handle})
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/videos?version=1"}, {http.MethodGet, "/videos/job-123?version=1"},
		{http.MethodDelete, "/videos/job-123?version=1"}, {http.MethodGet, "/videos/job-123/content?version=1"},
	} {
		req, err := http.NewRequestWithContext(t.Context(), target.method, "https://provider.example/v1"+target.path, nil)
		require.NoError(t, err)
		_, err = grant.Authorize(identity, material, catalogs.ProviderOperationVideosGenerations, req)
		require.NoError(t, err, target)
		req.Method = http.MethodPut
		_, err = grant.Authorize(identity, material, catalogs.ProviderOperationVideosGenerations, req)
		require.ErrorIs(t, err, credentials.ErrDestinationUnapproved)
	}
}
