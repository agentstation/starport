package routing

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountPolicyPrecedesCapabilityDisclosure(t *testing.T) {
	snapshot := contractSnapshot()
	for index := range snapshot.Candidates {
		snapshot.Candidates[index].Operations = []Operation{OperationChatCompletions}
		snapshot.Candidates[index].Endpoints = map[Operation]Endpoint{OperationChatCompletions: {Protocol: "openai", URL: "https://provider.test/chat/completions"}}
	}
	policy := AccountPolicy{Access: []ProviderAccess{{Provider: "provider-b", Models: []string{"author/primary"}}}}
	plan, err := NewPlanner().Plan(Request{Models: []string{"author/primary"}, Account: policy, Operation: OperationEmbeddings}, snapshot)
	require.ErrorIs(t, err, ErrOperationUnsupported)
	require.NotContains(t, err.Error(), "provider-a")
	require.Contains(t, err.Error(), "provider-b")
	for _, rejection := range plan.Rejections() {
		if rejection.Route.ProviderID == "provider-a" {
			require.Equal(t, RejectionAccountProvider, rejection.Code)
		}
	}
	for _, candidate := range snapshot.Candidates {
		permitted := candidate.Route.ProviderID == "provider-b" && candidate.Route.ModelID == "author/primary"
		require.Equal(t, permitted, policy.AllowsRoute(candidate.Route))
	}
}
