package proxy

import (
	"fmt"

	"github.com/agentstation/starport/internal/providers/connectors"
)

// TransformChatRequest converts the canonical use-case request at the provider boundary.
func TransformChatRequest(req *ChatCompletionRequest) (*connectors.ChatRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("canonical chat request is required")
	}
	return connectors.ChatRequestFromInference(req.Request)
}

// TransformChatResponse converts a connector ChatResponse to a proxy ChatCompletionResponse
func TransformChatResponse(resp *connectors.ChatResponse, modelUsed string) (*ChatCompletionResponse, error) {
	canonical, err := connectors.ChatResponseToInference(resp, modelUsed)
	if err != nil {
		return nil, fmt.Errorf("convert provider chat response to canonical inference: %w", err)
	}
	return &ChatCompletionResponse{Response: canonical}, nil
}

// TransformEmbeddingsRequest converts a proxy EmbeddingsRequest to a connector EmbeddingsRequest
func TransformEmbeddingsRequest(req *EmbeddingsRequest) *connectors.EmbeddingsRequest {
	return connectors.EmbeddingRequestFromInference(req.Request)
}
