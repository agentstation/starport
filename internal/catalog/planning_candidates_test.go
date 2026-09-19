package catalog

import (
	"testing"

	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestPlanningCandidatesOwnMutableValues(t *testing.T) {
	snapshot := &RoutableSnapshot{
		planningCandidates: []routing.Candidate{{
			Route:      routing.Route{ModelID: "author/model", ProviderID: "acme", ProviderModelID: "opaque"},
			Operations: []routing.Operation{routing.OperationChatCompletions},
			Endpoints: map[routing.Operation]routing.Endpoint{
				routing.OperationChatCompletions: {Protocol: "openai", URL: "https://provider.example"},
			},
			PromptCache: new(true), Capabilities: []string{"tools"},
			InputModalities: []routing.Modality{routing.ModalityText},
			Cost:            &routing.TokenCost{InputPerToken: 1, OutputPerToken: 2},
		}},
		planningByModel: map[string][]int{"author/model": {0}, "acme/opaque": {0}},
	}
	for _, unrestricted := range []bool{false, true} {
		first := snapshot.PlanningCandidates([]string{"author/model", "acme/opaque"}, unrestricted)
		require.Len(t, first, 1)
		first[0].Route.ModelID = "changed"
		first[0].Operations[0] = "changed"
		delete(first[0].Endpoints, routing.OperationChatCompletions)
		*first[0].PromptCache = false
		first[0].Capabilities[0] = "changed"
		first[0].InputModalities[0] = "changed"
		first[0].Cost.InputPerToken = 99
		next := snapshot.PlanningCandidates([]string{"author/model"}, unrestricted)
		require.Equal(t, snapshot.planningCandidates, next)
	}
	require.Empty(t, snapshot.PlanningCandidates([]string{"missing"}, false))
}
