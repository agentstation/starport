package routing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// modalitySnapshot holds three models that differ only in what they read: one
// text-only model, one that also reads audio, and one the catalog says
// nothing about. The third is the interesting one, because most offerings in
// the catalog still state no input modalities at all.
func modalitySnapshot() Snapshot {
	return Snapshot{
		CatalogGenerationID:  "catalog-generation-7",
		AvailabilityRevision: 11,
		Candidates: []Candidate{
			{
				Route: Route{
					CatalogGenerationID: "catalog-generation-7",
					ModelID:             "author/text-only",
					ProviderID:          "provider-a",
					ProviderModelID:     "text-only-a",
				},
				InputModalities: []Modality{ModalityText},
				ContextWindow:   32_000,
			},
			{
				Route: Route{
					CatalogGenerationID: "catalog-generation-7",
					ModelID:             "author/listens",
					ProviderID:          "provider-a",
					ProviderModelID:     "listens-a",
				},
				InputModalities: []Modality{ModalityText, ModalityAudio},
				ContextWindow:   32_000,
			},
			{
				Route: Route{
					CatalogGenerationID: "catalog-generation-7",
					ModelID:             "author/undescribed",
					ProviderID:          "provider-a",
					ProviderModelID:     "undescribed-a",
				},
				ContextWindow: 32_000,
			},
		},
	}
}

// TestModalityRouting holds the rule that gives this check its value: a model
// that cannot read the media the caller sent never receives the call. Before
// it, the request reached the provider and the provider's refusal came back
// as a gateway routing failure.
func TestModalityRouting(t *testing.T) {
	planner := NewPlanner()

	t.Run("a text-only model is refused by name", func(t *testing.T) {
		_, err := planner.Plan(Request{
			Models:             []string{"author/text-only"},
			RequiredModalities: []Modality{ModalityAudio},
		}, modalitySnapshot())

		require.ErrorIs(t, err, ErrModalityUnsupported)
		require.ErrorIs(t, err, ErrNoCandidate)
		require.Contains(t, err.Error(), "audio")
		require.Contains(t, err.Error(), "provider-a/text-only-a")
	})

	t.Run("the first modality the model misses is the one named", func(t *testing.T) {
		_, err := planner.Plan(Request{
			Models:             []string{"author/listens"},
			RequiredModalities: []Modality{ModalityAudio, ModalityVideo},
		}, modalitySnapshot())

		require.ErrorIs(t, err, ErrModalityUnsupported)
		require.Contains(t, err.Error(), "video")
		require.NotContains(t, err.Error(), "audio")
	})

	t.Run("a fallback model that cannot read the request is dropped", func(t *testing.T) {
		plan, err := planner.Plan(Request{
			Models:              []string{"author/text-only", "author/listens"},
			AllowModelFallbacks: true,
			RequiredModalities:  []Modality{ModalityAudio},
		}, modalitySnapshot())

		require.NoError(t, err)
		require.Equal(t, []string{"provider-a/listens-a"}, attemptIDs(plan.Attempts()))
		require.Contains(t, rejectionCodes(plan.Rejections()), RejectionMissingModality)
	})

	t.Run("a model that reads audio still routes", func(t *testing.T) {
		plan, err := planner.Plan(Request{
			Models:             []string{"author/listens"},
			RequiredModalities: []Modality{ModalityText, ModalityAudio},
		}, modalitySnapshot())

		require.NoError(t, err)
		require.Equal(t, []string{"provider-a/listens-a"}, attemptIDs(plan.Attempts()))
	})

	t.Run("catalog silence is not a refusal", func(t *testing.T) {
		plan, err := planner.Plan(Request{
			Models:             []string{"author/undescribed"},
			RequiredModalities: []Modality{ModalityAudio},
		}, modalitySnapshot())

		require.NoError(t, err)
		require.Equal(t, []string{"provider-a/undescribed-a"}, attemptIDs(plan.Attempts()))
	})

	t.Run("a request that carries no media routes anywhere", func(t *testing.T) {
		plan, err := planner.Plan(Request{
			Models: []string{"author/text-only"},
		}, modalitySnapshot())

		require.NoError(t, err)
		require.Equal(t, []string{"provider-a/text-only-a"}, attemptIDs(plan.Attempts()))
	})
}

// TestModalityRefusalKeepsTheOtherRejectionMessage proves the named refusal
// does not swallow the ordinary one. A plan that rejected every route for
// unrelated reasons must still report the route count, or an operator reading
// the error learns nothing about why nothing ran.
func TestModalityRefusalKeepsTheOtherRejectionMessage(t *testing.T) {
	_, err := NewPlanner().Plan(Request{
		Models:               []string{"author/text-only"},
		RequiredCapabilities: []string{"vision"},
	}, modalitySnapshot())

	require.ErrorIs(t, err, ErrNoCandidate)
	require.NotErrorIs(t, err, ErrModalityUnsupported)
	require.Contains(t, err.Error(), "1 route(s) rejected")
}
