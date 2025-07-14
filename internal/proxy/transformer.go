package proxy

import (
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
)

// TransformChatRequest converts a proxy ChatCompletionRequest to a connector ChatRequest
func TransformChatRequest(req *ChatCompletionRequest) *connectors.ChatRequest {
	connReq := &connectors.ChatRequest{
		Model:            req.Model,
		Messages:         req.Messages,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		Stream:           req.Stream,
		Stop:             req.Stop,
		MaxTokens:        req.MaxTokens,
		PresencePenalty:  req.PresencePenalty,
		FrequencyPenalty: req.FrequencyPenalty,
		User:             req.User,
		LogitBias:        convertLogitBias(req.LogitBias),
		Seed:             req.Seed,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,

		// OpenRouter extensions
		Models:    req.Models,
		Reasoning: transformReasoning(req.Reasoning),
	}

	// Note: Provider preferences would be handled at the routing layer
	// The connectors.ChatRequest doesn't have a Provider field

	return connReq
}

// TransformChatResponse converts a connector ChatResponse to a proxy ChatCompletionResponse
func TransformChatResponse(resp *connectors.ChatResponse, modelUsed string) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		ID:                resp.ID,
		Object:            resp.Object,
		Created:           resp.Created,
		Model:             resp.Model,
		Choices:           resp.Choices,
		Usage:             &resp.Usage,
		SystemFingerprint: resp.SystemFingerprint,
		ModelUsed:         modelUsed,
	}
}

// TransformEmbeddingsRequest converts a proxy EmbeddingsRequest to a connector EmbeddingsRequest
func TransformEmbeddingsRequest(req *EmbeddingsRequest) *connectors.EmbeddingsRequest {
	return &connectors.EmbeddingsRequest{
		Model:          req.Model,
		Input:          req.Input,
		EncodingFormat: req.EncodingFormat,
		Dimensions:     req.Dimensions,
		User:           req.User,
	}
}

// TransformEmbeddingsResponse converts a connector EmbeddingsResponse to a proxy EmbeddingsResponse
func TransformEmbeddingsResponse(resp *connectors.EmbeddingsResponse) *EmbeddingsResponse {
	return &EmbeddingsResponse{
		Object: resp.Object,
		Data:   resp.Data,
		Model:  resp.Model,
		Usage:  &resp.Usage,
	}
}

// TransformModelsToResponse converts connector models to a ModelsResponse
func TransformModelsToResponse(models []connectors.Model) *ModelsResponse {
	data := make([]ModelInfo, 0, len(models))

	for _, model := range models {
		info := ModelInfo{
			ID:      model.ID,
			Object:  "model",
			Created: model.Created,
			OwnedBy: model.OwnedBy,
		}

		// Basic models don't have extended metadata
		// That would come from the metadata package

		data = append(data, info)
	}

	return &ModelsResponse{
		Object: "list",
		Data:   data,
	}
}

// TransformProvidersToResponse converts provider metadata to a ProvidersResponse
func TransformProvidersToResponse(providers []connectors.ProviderMetadata) *ProvidersResponse {
	infos := make([]ProviderInfo, 0, len(providers))

	for _, provider := range providers {
		info := ProviderInfo{
			ID:           provider.Slug,
			Name:         provider.Name,
			Description:  fmt.Sprintf("Provider: %s", provider.Name),
			URL:          provider.PrivacyPolicyURL,
			RequiresAuth: true,
		}

		infos = append(infos, info)
	}

	return &ProvidersResponse{
		Providers: infos,
	}
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

// transformReasoning converts proxy ReasoningConfig to connectors ReasoningConfig
func transformReasoning(rc *ReasoningConfig) *connectors.ReasoningConfig {
	if rc == nil {
		return nil
	}
	return &connectors.ReasoningConfig{
		Effort:    rc.Effort,
		MaxTokens: rc.MaxTokens,
		Exclude:   rc.Exclude,
	}
}

// convertLogitBias converts float32 map to int map for compatibility
func convertLogitBias(bias map[string]float32) map[string]int {
	if bias == nil {
		return nil
	}

	result := make(map[string]int)
	for k, v := range bias {
		// Convert float32 to int (OpenAI uses -100 to 100 range)
		result[k] = int(v)
	}
	return result
}
