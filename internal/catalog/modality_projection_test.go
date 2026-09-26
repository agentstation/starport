package catalog

import (
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
	"testing"
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
