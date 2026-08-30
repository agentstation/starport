package connectors

import (
	"fmt"

	"github.com/agentstation/starport/internal/inference"
)

// ModerationRequestFromInference converts a canonical moderation request. The
// input slice is shared rather than copied: the executor hands one request to
// each attempt and no transport writes to it.
func ModerationRequestFromInference(request inference.ModerationRequest) *ModerationRequest {
	return &ModerationRequest{
		MediaTarget: MediaTarget{Model: request.Model},
		Inputs:      request.Inputs,
	}
}

// ModerationResponseToInference converts a provider moderation response.
func ModerationResponseToInference(response *ModerationResponse) (inference.ModerationResponse, error) {
	if response == nil {
		return inference.ModerationResponse{}, fmt.Errorf("moderation response is required")
	}
	results := make([]inference.ModerationResult, len(response.Results))
	for index, result := range response.Results {
		categories := make([]inference.ModerationCategory, len(result.Categories))
		for i, category := range result.Categories {
			categories[i] = inference.ModerationCategory{
				Name:    category.Name,
				Flagged: category.Flagged,
				Score:   category.Score,
			}
		}
		results[index] = inference.ModerationResult{
			Flagged:    result.Flagged,
			Categories: categories,
		}
	}
	return inference.ModerationResponse{
		ID:      response.ID,
		Model:   response.Model,
		Results: results,
		Usage:   mediaUsageToInference(response.Usage, 0),
	}, nil
}
