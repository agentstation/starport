package proxy

import (
	"fmt"
	"time"

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

// TransformEmbeddingsResponse converts a connector EmbeddingsResponse to a proxy EmbeddingsResponse
func TransformEmbeddingsResponse(resp *connectors.EmbeddingsResponse) (*EmbeddingsResponse, error) {
	canonical, err := connectors.EmbeddingResponseToInference(resp)
	if err != nil {
		return nil, fmt.Errorf("convert provider embedding response to canonical inference: %w", err)
	}
	return &EmbeddingsResponse{Response: canonical}, nil
}

// GenerateCompletionID generates a unique ID for a completion
func GenerateCompletionID() string {
	// Use the same format as OpenAI: chatcmpl-{random}
	return "chatcmpl-" + generateRandomString(29)
}

// generateRandomString generates a random string of the specified length
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
