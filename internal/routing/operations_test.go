package routing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestACatalogOperationOutsideTheSetIsInertRatherThanFatal holds the rule that
// decision MOD-D6 states. A catalog generation names operations a build may not
// serve, and the old guard answered a single unknown name by rejecting the
// whole snapshot. That answer took chat and embeddings routing down with it for
// every provider in the generation.
func TestACatalogOperationOutsideTheSetIsInertRatherThanFatal(t *testing.T) {
	const generation = "catalog-generation-unnamed-operation"
	const unnamed Operation = "video-generations"
	require.False(t, ServedOperations().Contains(unnamed))

	chatCandidate := Candidate{
		Route: Route{
			CatalogGenerationID: generation,
			ModelID:             "author/chat-model",
			ProviderID:          "provider",
			ProviderModelID:     "opaque/chat@001",
		},
		Operations: []Operation{OperationChatCompletions},
		Endpoints: map[Operation]Endpoint{
			OperationChatCompletions: {Protocol: "openai", URL: "https://provider.test/chat"},
		},
	}
	// The unnamed operation carries no endpoint, which is the shape a build
	// that cannot serve it produces.
	futureCandidate := Candidate{
		Route: Route{
			CatalogGenerationID: generation,
			ModelID:             "author/video-model",
			ProviderID:          "provider",
			ProviderModelID:     "opaque/video@001",
		},
		Operations: []Operation{unnamed},
	}

	plan, err := NewPlanner().Plan(
		Request{Models: []string{"author/chat-model"}, Operation: OperationChatCompletions},
		Snapshot{
			CatalogGenerationID: generation,
			Candidates:          []Candidate{chatCandidate, futureCandidate},
		},
	)
	require.NoError(t, err)
	require.Len(t, plan.Attempts(), 1)
	require.Equal(t, "provider/opaque/chat@001", plan.Attempts()[0].Route.ID())
}

// TestAnUnnamedOperationNeverRoutes is the other half of the same rule. Inert
// must not mean reachable.
func TestAnUnnamedOperationNeverRoutes(t *testing.T) {
	const unnamed Operation = "video-generations"
	_, err := NewPlanner().Plan(
		Request{Models: []string{"author/video-model"}, Operation: unnamed},
		Snapshot{CatalogGenerationID: "generation"},
	)
	require.ErrorIs(t, err, ErrInvalidRequest)
}

// TestThePlannerRefusesAMediaRequestToAChatModel proves the refusal happens in
// the plan, before any provider call. A chat model that answered an image
// request would spend a credential and a round trip to learn what the catalog
// already states.
func TestThePlannerRefusesAMediaRequestToAChatModel(t *testing.T) {
	const generation = "catalog-generation-media-refusal"
	candidate := Candidate{
		Route: Route{
			CatalogGenerationID: generation,
			ModelID:             "author/chat-model",
			ProviderID:          "provider",
			ProviderModelID:     "opaque/chat@001",
		},
		Operations: []Operation{OperationChatCompletions},
		Endpoints: map[Operation]Endpoint{
			OperationChatCompletions: {Protocol: "openai", URL: "https://provider.test/chat"},
		},
	}
	snapshot := Snapshot{CatalogGenerationID: generation, Candidates: []Candidate{candidate}}

	plan, err := NewPlanner().Plan(
		Request{Models: []string{"author/chat-model"}, Operation: OperationImagesGenerations},
		snapshot,
	)
	require.ErrorIs(t, err, ErrNoCandidate)
	require.Equal(t, []RejectionCode{RejectionMissingOperation}, rejectionCodes(plan.Rejections()))
	require.Empty(t, plan.Attempts())

	// The same model with the same catalog facts still answers a chat request,
	// so the refusal is scoped to the operation rather than to the route.
	plan, err = NewPlanner().Plan(
		Request{Models: []string{"author/chat-model"}, Operation: OperationChatCompletions},
		snapshot,
	)
	require.NoError(t, err)
	require.Len(t, plan.Attempts(), 1)
}

// TestAMediaModelRoutesItsOwnOperation completes the pair. A media route is
// reachable for the operation it declares and for no other.
func TestAMediaModelRoutesItsOwnOperation(t *testing.T) {
	const generation = "catalog-generation-media-route"
	speech := Endpoint{Protocol: "openai", URL: "https://provider.test/audio/speech"}
	candidate := Candidate{
		Route: Route{
			CatalogGenerationID: generation,
			ModelID:             "author/speech-model",
			ProviderID:          "provider",
			ProviderModelID:     "opaque/speech@001",
		},
		Operations: []Operation{OperationAudioSpeech},
		Endpoints:  map[Operation]Endpoint{OperationAudioSpeech: speech},
	}
	snapshot := Snapshot{CatalogGenerationID: generation, Candidates: []Candidate{candidate}}

	plan, err := NewPlanner().Plan(
		Request{Models: []string{"author/speech-model"}, Operation: OperationAudioSpeech},
		snapshot,
	)
	require.NoError(t, err)
	require.Equal(t, OperationAudioSpeech, plan.Attempts()[0].Route.Operation)
	require.Equal(t, speech, plan.Attempts()[0].Route.Endpoint)

	_, err = NewPlanner().Plan(
		Request{Models: []string{"author/speech-model"}, Operation: OperationChatCompletions},
		snapshot,
	)
	require.ErrorIs(t, err, ErrNoCandidate)
}

// TestTheOperationSetNamesEveryOperationTheGatewayPlans pins the membership
// itself. The set is read by three separate guards, so a name added to one and
// missed by another is the defect this test exists to catch.
func TestTheOperationSetNamesEveryOperationTheGatewayPlans(t *testing.T) {
	require.Equal(t, []Operation{
		OperationAudioSpeech,
		OperationAudioTranscriptions,
		OperationAudioTranslations,
		OperationChatCompletions,
		OperationEmbeddings,
		OperationImagesEdits,
		OperationImagesGenerations,
		OperationVideosGenerations,
	}, ServedOperations().Members())
	require.False(t, ServedOperations().Contains(""))
	require.Equal(t, 8, ServedOperations().Len())
}
