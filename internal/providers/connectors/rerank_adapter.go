package connectors

import (
	"fmt"

	"github.com/agentstation/starport/internal/inference"
)

// RerankRequestFromInference converts a canonical rerank request. The document
// slice is shared rather than copied: the executor hands one request to each
// attempt and no transport writes to it, so a copy per attempt would duplicate
// a batch that can reach a thousand documents.
func RerankRequestFromInference(request inference.RerankRequest) *RerankRequest {
	return &RerankRequest{
		MediaTarget:          MediaTarget{Model: request.Model},
		Query:                request.Query,
		Documents:            request.Documents,
		TopN:                 request.TopN,
		MaxTokensPerDocument: request.MaxTokensPerDocument,
	}
}

// RerankResponseToInference converts a provider rerank response. It carries no
// document text, because a result names a position in the request and the
// request is the one copy the gateway holds.
func RerankResponseToInference(response *RerankResponse) (inference.RerankResponse, error) {
	if response == nil {
		return inference.RerankResponse{}, fmt.Errorf("rerank response is required")
	}
	results := make([]inference.RerankResult, len(response.Results))
	for index, result := range response.Results {
		results[index] = inference.RerankResult{
			Index:          result.Index,
			RelevanceScore: result.RelevanceScore,
		}
	}
	usage := mediaUsageToInference(response.Usage, 0)
	usage.SearchUnits = response.SearchUnits
	return inference.RerankResponse{Results: results, Usage: usage}, nil
}
