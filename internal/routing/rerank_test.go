package routing

import (
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

const rerankGeneration = "catalog-generation-rerank"

// rerankCandidate builds one rerank offering of the shared model. The document
// bound is the field that differs between two offerings of one model, so it is
// the argument rather than a constant.
func rerankCandidate(provider, providerModelID string, maxDocuments int) Candidate {
	return Candidate{
		Route: Route{
			CatalogGenerationID: rerankGeneration,
			ModelID:             "author/rerank-model",
			ProviderID:          provider,
			ProviderModelID:     providerModelID,
		},
		Operations: []Operation{OperationRerank},
		Endpoints: map[Operation]Endpoint{
			OperationRerank: {Protocol: "cohere", URL: "https://" + provider + ".test/v2/rerank"},
		},
		MaxDocuments: maxDocuments,
	}
}

// TestThePlannerRefusesARerankRequestToAChatModel holds invariant R7. A chat
// model reached by a rerank request would take a credential and a round trip to
// learn what the catalog already states, and the provider's own refusal names a
// wire field rather than the real cause.
func TestThePlannerRefusesARerankRequestToAChatModel(t *testing.T) {
	chat := Candidate{
		Route: Route{
			CatalogGenerationID: rerankGeneration,
			ModelID:             "author/chat-model",
			ProviderID:          "provider",
			ProviderModelID:     "opaque/chat@001",
		},
		Operations: []Operation{OperationChatCompletions},
		Endpoints: map[Operation]Endpoint{
			OperationChatCompletions: {Protocol: "openai", URL: "https://provider.test/chat"},
		},
	}
	snapshot := Snapshot{CatalogGenerationID: rerankGeneration, Candidates: []Candidate{chat}}

	plan, err := NewPlanner().Plan(
		Request{Models: []string{"author/chat-model"}, Operation: OperationRerank},
		snapshot,
	)
	require.Empty(t, plan.Attempts())
	require.ErrorIs(t, err, ErrNoCandidate)

	// The typed refusal is the point. Without it the caller hears "no
	// candidate", which reads as a gateway that is short of capacity and
	// invites the retry that will fail the same way.
	require.ErrorIs(t, err, ErrOperationUnsupported)
	require.NotErrorIs(t, err, ErrModalityUnsupported)

	// The message names the model and the operation, so an operator reading one
	// log line knows which of the two to change.
	require.Contains(t, err.Error(), "provider/opaque/chat@001")
	require.Contains(t, err.Error(), string(OperationRerank))
}

// TestARerankModelPlansEveryOfferingThatServesIt is the other half. A refusal
// that fired for every model would satisfy the test above and route nothing, so
// this one asserts that two rerank offerings of one model both plan, in the
// order the planner states.
func TestARerankModelPlansEveryOfferingThatServesIt(t *testing.T) {
	plan, err := NewPlanner().Plan(
		Request{Models: []string{"author/rerank-model"}, Operation: OperationRerank},
		Snapshot{
			CatalogGenerationID: rerankGeneration,
			Candidates: []Candidate{
				rerankCandidate("voyage", "rerank-2.5", 1000),
				rerankCandidate("cohere", "rerank-v3.5", 100),
			},
		},
	)
	require.NoError(t, err)

	attempts := plan.Attempts()
	require.Len(t, attempts, 2)
	require.Equal(t, []string{"cohere/rerank-v3.5", "voyage/rerank-2.5"},
		[]string{attempts[0].Route.ID(), attempts[1].Route.ID()})
	for _, attempt := range attempts {
		require.Equal(t, OperationRerank, attempt.Route.Operation)
		require.NotEmpty(t, attempt.Route.Endpoint.URL)
	}
}

// TestTheDocumentBoundRidesOnTheChosenRoute pins where the bound travels. The
// planner does not read it, so the only thing that makes it useful is arriving
// on the attempt that names the offering it came from. The two offerings above
// state different bounds, which is why a single number on the model would be
// wrong.
func TestTheDocumentBoundRidesOnTheChosenRoute(t *testing.T) {
	plan, err := NewPlanner().Plan(
		Request{Models: []string{"author/rerank-model"}, Operation: OperationRerank},
		Snapshot{
			CatalogGenerationID: rerankGeneration,
			Candidates: []Candidate{
				rerankCandidate("voyage", "rerank-2.5", 1000),
				rerankCandidate("cohere", "rerank-v3.5", 100),
			},
		},
	)
	require.NoError(t, err)

	bounds := map[string]int{}
	for _, attempt := range plan.Attempts() {
		bounds[attempt.Route.ID()] = attempt.Route.MaxDocuments
	}
	require.Equal(t, map[string]int{"cohere/rerank-v3.5": 100, "voyage/rerank-2.5": 1000}, bounds)
}

// TestAnUnstatedDocumentBoundIsSilenceRatherThanZero covers the offering whose
// catalog entry states no bound. Reading zero as "no documents allowed" would
// refuse every request to a model the catalog has not described yet.
func TestAnUnstatedDocumentBoundIsSilenceRatherThanZero(t *testing.T) {
	plan, err := NewPlanner().Plan(
		Request{Models: []string{"author/rerank-model"}, Operation: OperationRerank},
		Snapshot{
			CatalogGenerationID: rerankGeneration,
			Candidates:          []Candidate{rerankCandidate("jina", "jina-reranker-v2", 0)},
		},
	)
	require.NoError(t, err)
	require.Len(t, plan.Attempts(), 1)
	require.Zero(t, plan.Attempts()[0].Route.MaxDocuments)
}

// TestASnapshotWithANegativeDocumentBoundIsInvalid keeps the bound in the same
// class as the context window. A negative bound reaches the rerank path as a
// limit no list satisfies, and a snapshot is the place to catch it.
func TestASnapshotWithANegativeDocumentBoundIsInvalid(t *testing.T) {
	_, err := NewPlanner().Plan(
		Request{Models: []string{"author/rerank-model"}, Operation: OperationRerank},
		Snapshot{
			CatalogGenerationID: rerankGeneration,
			Candidates:          []Candidate{rerankCandidate("cohere", "rerank-v3.5", -1)},
		},
	)
	require.ErrorIs(t, err, ErrInvalidSnapshot)
}

// TestTheRerankOperationSpellsTheCatalogName pins this package's vocabulary to
// Starmap's. The package keeps its own Operation type so that a plan stays a
// pure function of the values handed to it, and the cost of that copy is that
// one renamed operation in the catalog would silently stop matching. The
// comparison below is what makes the copy safe rather than hopeful.
func TestTheRerankOperationSpellsTheCatalogName(t *testing.T) {
	require.Equal(t, string(catalogs.ProviderOperationRerank), string(OperationRerank))
	require.True(t, ServedOperations().Contains(Operation(catalogs.ProviderOperationRerank)))
}
