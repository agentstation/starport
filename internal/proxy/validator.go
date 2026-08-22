package proxy

import (
	"fmt"
	"strings"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/inference"
)

// ValidateChatCompletionRequest validates one canonical gateway chat request.
func ValidateChatCompletionRequest(req *ChatCompletionRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	request := req.Request
	if request.Model == "" && len(request.FallbackModels) == 0 {
		return validationError("model", "either 'model' or 'models' must be specified")
	}
	if len(request.Messages) == 0 {
		return validationError("messages", "messages array cannot be empty")
	}
	for index, message := range request.Messages {
		field := fmt.Sprintf("messages[%d]", index)
		switch message.Role {
		case inference.RoleSystem, inference.RoleUser, inference.RoleAssistant, inference.RoleTool:
		default:
			return validationError(field+".role", "role must be system, user, assistant, or tool")
		}
		if message.Role == inference.RoleTool && message.ToolCallID == "" {
			return validationError(field+".tool_call_id", "tool_call_id is required for tool messages")
		}
		for partIndex, part := range message.Content {
			if part.CacheControl != "" && part.CacheControl != "ephemeral" {
				return validationError(
					fmt.Sprintf("%s.content[%d].cache_control.type", field, partIndex),
					"cache control type must be ephemeral",
				)
			}
		}
	}
	if value := request.Sampling.Temperature; value != nil && (*value < 0 || *value > 2) {
		return validationError("temperature", "temperature must be between 0 and 2")
	}
	if value := request.Sampling.TopP; value != nil && (*value < 0 || *value > 1) {
		return validationError("top_p", "top_p must be between 0 and 1")
	}
	if value := request.Sampling.CandidateCount; value != nil && *value < 1 {
		return validationError("n", "n must be at least 1")
	}
	if value := request.Sampling.MaxTokens; value != nil && *value < 1 {
		return validationError("max_tokens", "max_tokens must be at least 1")
	}
	if value := request.Sampling.PresencePenalty; value != nil && (*value < -2 || *value > 2) {
		return validationError("presence_penalty", "presence_penalty must be between -2 and 2")
	}
	if value := request.Sampling.FrequencyPenalty; value != nil && (*value < -2 || *value > 2) {
		return validationError("frequency_penalty", "frequency_penalty must be between -2 and 2")
	}
	if req.Route != "" && req.Route != "fallback" {
		return validationError("route", "supported route is fallback")
	}
	return nil
}

// ValidateEmbeddingsRequest validates one canonical embedding request.
func ValidateEmbeddingsRequest(req *EmbeddingsRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	request := req.Request
	if request.Model == "" {
		return validationError("model", "model is required")
	}
	textInputs := len(request.Input.Texts)
	tokenInputs := len(request.Input.TokenIDs)
	if textInputs == 0 && tokenInputs == 0 {
		return validationError("input", "input is required")
	}
	if textInputs > 0 && tokenInputs > 0 {
		return validationError("input", "input must use text or token IDs, not both")
	}
	for index, input := range request.Input.Texts {
		if input == "" {
			return validationError(fmt.Sprintf("input[%d]", index), "input string cannot be empty")
		}
	}
	for index, input := range request.Input.TokenIDs {
		if len(input) == 0 {
			return validationError(fmt.Sprintf("input[%d]", index), "token input cannot be empty")
		}
	}
	if request.EncodingFormat != "" && request.EncodingFormat != "float" && request.EncodingFormat != "base64" {
		return validationError("encoding_format", "encoding_format must be float or base64")
	}
	if request.Dimensions != nil && *request.Dimensions < 1 {
		return validationError("dimensions", "dimensions must be at least 1")
	}
	return nil
}

func validationError(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

// ExtractProviderFromModel extracts a provider-scoped model ID.
func ExtractProviderFromModel(modelID string) (provider, model string) {
	provider, model, ok := runtimecatalog.SplitModelID(modelID)
	if ok {
		return provider, model
	}
	return "", strings.TrimSpace(modelID)
}
