package routing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const moderationGeneration = "catalog-generation-moderations"

// TestThePlannerRefusesAModerationRequestToAChatModel holds the planning half
// of ENR-V15. A chat model reached by a moderation request would take a
// credential and a round trip to learn what the catalog already states.
func TestThePlannerRefusesAModerationRequestToAChatModel(t *testing.T) {
	chat := Candidate{
		Route: Route{
			CatalogGenerationID: moderationGeneration,
			ModelID:             "author/chat-model",
			ProviderID:          "provider",
			ProviderModelID:     "opaque/chat@001",
		},
		Operations: []Operation{OperationChatCompletions},
		Endpoints: map[Operation]Endpoint{
			OperationChatCompletions: {Protocol: "openai", URL: "https://provider.test/chat"},
		},
	}

	plan, err := NewPlanner().Plan(
		Request{Models: []string{"author/chat-model"}, Operation: OperationModerations},
		Snapshot{CatalogGenerationID: moderationGeneration, Candidates: []Candidate{chat}},
	)
	require.Empty(t, plan.Attempts())
	require.ErrorIs(t, err, ErrNoCandidate)
	require.ErrorIs(t, err, ErrOperationUnsupported)
	require.Contains(t, err.Error(), "provider/opaque/chat@001")
	require.Contains(t, err.Error(), string(OperationModerations))
}

// TestAModerationModelPlansItsOffering is the other half: the offering the
// catalog names for the operation actually plans.
func TestAModerationModelPlansItsOffering(t *testing.T) {
	candidate := Candidate{
		Route: Route{
			CatalogGenerationID: moderationGeneration,
			ModelID:             "openai/omni-moderation-latest",
			ProviderID:          "openai",
			ProviderModelID:     "omni-moderation-latest",
		},
		Operations: []Operation{OperationModerations},
		Endpoints: map[Operation]Endpoint{
			OperationModerations: {Protocol: "openai", URL: "https://openai.test/v1/moderations"},
		},
	}

	plan, err := NewPlanner().Plan(
		Request{Models: []string{"openai/omni-moderation-latest"}, Operation: OperationModerations},
		Snapshot{CatalogGenerationID: moderationGeneration, Candidates: []Candidate{candidate}},
	)
	require.NoError(t, err)
	require.Len(t, plan.Attempts(), 1)
	require.Equal(t, "openai", plan.Attempts()[0].Route.ProviderID)
}
