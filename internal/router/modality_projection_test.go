package router

import (
	"errors"
	"fmt"
	"testing"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

func modalityDefinition(input ...starmapcatalogs.ModelModality) starmapcatalogs.ModelDefinition {
	return starmapcatalogs.ModelDefinition{
		Capabilities: starmapcatalogs.ModelDefinitionCapabilities{
			Features: &starmapcatalogs.ModelFeatures{
				Modalities: starmapcatalogs.ModelModalities{Input: input},
			},
		},
	}
}

// TestModelInputModalitiesProjection holds the one translation between the
// two vocabularies. Starmap records a document as the pdf modality, and a
// projection that carried the catalog word through would reject every
// document request against every model.
func TestModelInputModalitiesProjection(t *testing.T) {
	cases := []struct {
		name       string
		definition starmapcatalogs.ModelDefinition
		want       []routing.Modality
	}{
		{
			name:       "pdf becomes document",
			definition: modalityDefinition(starmapcatalogs.ModelModalityPDF),
			want:       []routing.Modality{routing.ModalityDocument},
		},
		{
			name: "every named modality carries",
			definition: modalityDefinition(
				starmapcatalogs.ModelModalityText,
				starmapcatalogs.ModelModalityImage,
				starmapcatalogs.ModelModalityAudio,
				starmapcatalogs.ModelModalityVideo,
			),
			want: []routing.Modality{
				routing.ModalityText,
				routing.ModalityImage,
				routing.ModalityAudio,
				routing.ModalityVideo,
			},
		},
		{
			name:       "a modality the planner cannot name is dropped",
			definition: modalityDefinition(starmapcatalogs.ModelModalityEmbedding),
			want:       nil,
		},
		{
			name:       "a model with no stated features states no modalities",
			definition: starmapcatalogs.ModelDefinition{},
			want:       nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, modelInputModalities(testCase.definition))
		})
	}
}

// TestPlanningModalitiesCarryRequestNames proves the request side of the same
// boundary. The proxy derives modality names from the message content, and
// the planner receives them as its own type without a second translation.
func TestPlanningModalitiesCarryRequestNames(t *testing.T) {
	require.Equal(
		t,
		[]routing.Modality{routing.ModalityAudio, routing.ModalityDocument},
		planningModalities([]string{"audio", "document"}),
	)
	require.Nil(t, planningModalities(nil))
}

// TestRoutePlanFailureKeepsTheModalityRefusal holds the classification the
// caller depends on. Collapsing every planner failure onto ErrNoModelsAvailable
// turned a caller mistake into a 503, which told the caller to retry a request
// that can never succeed against that model.
func TestRoutePlanFailureKeepsTheModalityRefusal(t *testing.T) {
	refusal := fmt.Errorf(
		"%w: %w: openai/gpt-4o@openai: model does not read audio input",
		routing.ErrNoCandidate, routing.ErrModalityUnsupported,
	)

	mapped := routePlanFailure(refusal)
	require.ErrorIs(t, mapped, routing.ErrModalityUnsupported)
	require.Contains(t, mapped.Error(), "audio")

	mapped = routePlanFailure(fmt.Errorf("%w: 3 route(s) rejected", routing.ErrNoCandidate))
	require.ErrorIs(t, mapped, ErrNoModelsAvailable)
	require.NotErrorIs(t, mapped, routing.ErrModalityUnsupported)

	require.Nil(t, routePlanFailure(errors.New("snapshot unavailable")))
}
