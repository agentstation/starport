package credentials

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func destinationFixture(t *testing.T) (DestinationIdentity, Material, *DestinationGrant, *http.Request) {
	t.Helper()
	identity := DestinationIdentity{Provider: "acme", Role: "fixture-role", Handle: "opaque-handle"}
	profile := catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Fields:     []catalogs.ProviderCredentialFieldID{"api-key"},
		Placements: []catalogs.ProviderCredentialPlacement{{Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader, Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer}},
	}
	material := NewMaterial(profile, map[catalogs.ProviderCredentialFieldID]string{"api-key": "fixture-secret"}, MaterialMetadata{})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://provider.example/v1/chat?version=1", nil)
	require.NoError(t, err)
	grant, err := NewDestinationGrant(identity, profile, []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: request.Method, URL: request.URL.String()}})
	require.NoError(t, err)
	return identity, material, grant, request
}

func TestDestinationGrantRefusesChangedContract(t *testing.T) {
	for _, change := range []string{"provider", "role", "handle", "operation", "host", "scheme", "port", "path", "query", "method", "host-header", "placement", "primitive", "scope", "profile", "userinfo", "fragment"} {
		t.Run(change, func(t *testing.T) {
			identity, material, grant, request := destinationFixture(t)
			operation := catalogs.ProviderOperationChatCompletions
			profile := material.Profile()
			switch change {
			case "provider":
				identity.Provider = "other"
			case "role":
				identity.Role = "other"
			case "handle":
				identity.Handle = "other"
			case "operation":
				operation = catalogs.ProviderOperationEmbeddings
			case "host":
				request.URL.Host = "other.example"
			case "scheme":
				request.URL.Scheme = "http"
			case "port":
				request.URL.Host += ":8443"
			case "path":
				request.URL.Path = "/other"
			case "query":
				request.URL.RawQuery = "version=2"
			case "method":
				request.Method = http.MethodGet
			case "host-header":
				request.Host = "other.example"
			case "placement":
				profile.Placements[0].Name = "X-Other-Key"
			case "primitive":
				profile.Primitive = catalogs.ProviderAuthenticationBearerToken
			case "scope":
				profile.Scopes = []string{"new-scope"}
			case "profile":
				profile.ID = "other-profile"
			case "userinfo":
				request.URL.User = url.User("other")
			case "fragment":
				request.URL.Fragment = "other"
			}
			material = NewMaterial(profile, map[catalogs.ProviderCredentialFieldID]string{"api-key": "fixture-secret"}, MaterialMetadata{})
			authorization, err := grant.Authorize(identity, material, operation, request)
			require.ErrorIs(t, err, ErrDestinationUnapproved)
			require.ErrorIs(t, authorization.Check(request), ErrDestinationUnapproved)
			require.Empty(t, request.Header.Get("Authorization"))
		})
	}
}

func TestDestinationGrantApprovalAndRevocation(t *testing.T) {
	identity, material, grant, request := destinationFixture(t)
	authorization, err := grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
	require.NoError(t, err)
	require.NoError(t, authorization.Check(request))
	changed := request.Clone(t.Context())
	changed.URL.Host = "127.0.0.1:8123"
	changed.Host = changed.URL.Host
	changed.URL.Scheme = "http"
	require.ErrorIs(t, authorization.Check(changed), ErrDestinationUnapproved)
	local, err := NewDestinationGrant(identity, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: changed.Method, URL: changed.URL.String()}})
	require.NoError(t, err)
	approved, err := local.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, changed)
	require.NoError(t, err)
	require.NoError(t, approved.Check(changed))
	require.ErrorIs(t, approved.Check(request), ErrDestinationUnapproved)
	local.Revoke()
	require.ErrorIs(t, approved.Check(changed), ErrDestinationUnapproved)
	_, err = local.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, changed)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	require.NoError(t, authorization.Check(request))
}

func TestDestinationGrantCopiesApproval(t *testing.T) {
	identity, material, _, request := destinationFixture(t)
	profile := material.Profile()
	destinations := []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: request.Method, URL: request.URL.String()}}
	grant, err := NewDestinationGrant(identity, profile, destinations)
	require.NoError(t, err)
	profile.Placements[0].Name = "Other"
	destinations[0].URL = "https://other.example"
	_, err = grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
	require.NoError(t, err)
}

func TestDestinationAuthorizationCheckHasNoAllocations(t *testing.T) {
	identity, material, grant, request := destinationFixture(t)
	authorization, err := grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
	require.NoError(t, err)
	allocations := testing.AllocsPerRun(100, func() {
		if _, err := grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request); err != nil {
			panic(err)
		}
		if err := authorization.Check(request); err != nil {
			panic(err)
		}
	})
	require.Zero(t, allocations)
}

func TestDestinationGrantRejectsInvalidApproval(t *testing.T) {
	identity, material, _, _ := destinationFixture(t)
	for _, target := range []string{"", "/relative", "ftp://provider.example", "https://user:pass@provider.example/v1", "https://provider.example/v1#fragment"} {
		_, err := NewDestinationGrant(identity, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: http.MethodPost, URL: target}})
		require.ErrorIs(t, err, ErrDestinationUnapproved)
	}
	var grant *DestinationGrant
	_, err := grant.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, nil)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	var authorization *DestinationAuthorization
	require.ErrorIs(t, authorization.Check(nil), ErrDestinationUnapproved)
}

func TestDestinationGrantRequiresApprovalForNewPlacement(t *testing.T) {
	identity, material, grant, request := destinationFixture(t)
	profile := material.Profile()
	profile.Placements[0].Kind = catalogs.ProviderCredentialPlacementQuery
	profile.Placements[0].Name = "key"
	profile.Placements[0].Scheme = catalogs.ProviderCredentialSchemeDirect
	changed := NewMaterial(profile, map[catalogs.ProviderCredentialFieldID]string{"api-key": "fixture-secret"}, MaterialMetadata{})
	_, err := grant.Authorize(identity, changed, catalogs.ProviderOperationChatCompletions, request)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	approved, err := NewDestinationGrant(identity, profile, []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: request.Method, URL: request.URL.String()}})
	require.NoError(t, err)
	authorization, err := approved.Authorize(identity, changed, catalogs.ProviderOperationChatCompletions, request)
	require.NoError(t, err)
	require.NoError(t, authorization.Check(request))
	_, err = approved.Authorize(identity, material, catalogs.ProviderOperationChatCompletions, request)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
}
