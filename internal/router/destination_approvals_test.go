package router

import (
	"net/http"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/stretchr/testify/require"
)

func TestRouterSelectsApprovalsForActualCredentialRole(t *testing.T) {
	fixture := newEndpointBindingFixture(t)
	material := fixture.operator.material
	identity := credentials.DestinationIdentity{Provider: "acme", Role: string(keyring.SourceEnvironment), Handle: material.Handle()}
	target := "https://operator.example/projects/operator-project/models/opaque/model@001/chat/completions"
	grant, err := credentials.NewDestinationGrant(identity, material.Profile(), []credentials.Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: http.MethodPost, URL: target}})
	require.NoError(t, err)
	approvals, err := credentials.NewDestinationApprovals([]*credentials.DestinationGrant{grant})
	require.NoError(t, err)
	WithDestinationApprovals(approvals)(fixture.router.(*modelRouter))
	_, err = fixture.router.RouteWithFallback(t.Context(), fixture.request(keyring.OperatorFirst))
	require.NoError(t, err)
	require.Len(t, fixture.seen, 1)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, nil)
	require.NoError(t, err)
	_, err = fixture.seen[0].material.AuthorizeDestination(request)
	require.NoError(t, err)
	_, err = fixture.router.RouteWithFallback(t.Context(), fixture.request(keyring.BYOKOnly))
	require.Error(t, err)
	require.Len(t, fixture.seen, 1, "an operator grant must not approve account material")
	grant.Revoke()
	_, err = fixture.router.RouteWithFallback(t.Context(), fixture.request(keyring.OperatorFirst))
	require.Error(t, err)
	require.Len(t, fixture.seen, 1, "revoked approval must stop before connector invocation")
}

func TestRouterExplicitEmptyApprovalsDenyAll(t *testing.T) {
	fixture := newEndpointBindingFixture(t)
	WithDestinationApprovals(nil)(fixture.router.(*modelRouter))
	_, err := fixture.router.RouteWithFallback(t.Context(), fixture.request(keyring.OperatorFirst))
	require.Error(t, err)
	require.Empty(t, fixture.seen)
}

func TestRouterFallsBackToApprovedCredentialRole(t *testing.T) {
	fixture := newEndpointBindingFixture(t)
	identity := credentials.DestinationIdentity{Provider: "acme", Role: string(keyring.SourceBYOK), Handle: "account"}
	target := "https://account.example/projects/account-project/models/opaque/model@001/chat/completions"
	grant, err := credentials.NewDestinationGrant(identity, fixture.operator.material.Profile(), []credentials.Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: http.MethodPost, URL: target}})
	require.NoError(t, err)
	approvals, err := credentials.NewDestinationApprovals([]*credentials.DestinationGrant{grant})
	require.NoError(t, err)
	WithDestinationApprovals(approvals)(fixture.router.(*modelRouter))
	_, err = fixture.router.RouteWithFallback(t.Context(), fixture.request(keyring.OperatorFirst))
	require.NoError(t, err)
	require.Equal(t, []string{"account"}, fixture.materialVersions())
	require.Equal(t, []string{target}, fixture.endpoints())
}
