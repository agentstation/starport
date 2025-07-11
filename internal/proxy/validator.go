package proxy

import (
	"fmt"
	"strings"
)

// ValidateChatCompletionRequest validates a chat completion request
func ValidateChatCompletionRequest(req *ChatCompletionRequest) error {
	// Check that either model or models is specified
	if req.Model == "" && len(req.Models) == 0 {
		return &ValidationError{
			Field:   "model",
			Message: "either 'model' or 'models' must be specified",
		}
	}

	// Validate messages
	if len(req.Messages) == 0 {
		return &ValidationError{
			Field:   "messages",
			Message: "messages array cannot be empty",
		}
	}

	// Validate each message
	for i, msg := range req.Messages {
		if msg.Role == "" {
			return &ValidationError{
				Field:   fmt.Sprintf("messages[%d].role", i),
				Message: "role is required",
			}
		}

		// Validate role is one of the allowed values
		validRoles := map[string]bool{
			"system":    true,
			"user":      true,
			"assistant": true,
			"tool":      true,
		}

		if !validRoles[msg.Role] {
			return &ValidationError{
				Field:   fmt.Sprintf("messages[%d].role", i),
				Message: fmt.Sprintf("invalid role '%s', must be one of: system, user, assistant, tool", msg.Role),
			}
		}

		// Validate content based on role
		if msg.Role == "tool" && msg.ToolCallID == "" {
			return &ValidationError{
				Field:   fmt.Sprintf("messages[%d].tool_call_id", i),
				Message: "tool_call_id is required for tool messages",
			}
		}
	}

	// Validate temperature if provided
	if req.Temperature != nil {
		if *req.Temperature < 0 || *req.Temperature > 2 {
			return &ValidationError{
				Field:   "temperature",
				Message: "temperature must be between 0 and 2",
			}
		}
	}

	// Validate top_p if provided
	if req.TopP != nil {
		if *req.TopP < 0 || *req.TopP > 1 {
			return &ValidationError{
				Field:   "top_p",
				Message: "top_p must be between 0 and 1",
			}
		}
	}

	// Validate n if provided
	if req.N != nil && *req.N < 1 {
		return &ValidationError{
			Field:   "n",
			Message: "n must be at least 1",
		}
	}

	// Validate max_tokens if provided
	if req.MaxTokens != nil && *req.MaxTokens < 1 {
		return &ValidationError{
			Field:   "max_tokens",
			Message: "max_tokens must be at least 1",
		}
	}

	// Validate presence_penalty if provided
	if req.PresencePenalty != nil {
		if *req.PresencePenalty < -2 || *req.PresencePenalty > 2 {
			return &ValidationError{
				Field:   "presence_penalty",
				Message: "presence_penalty must be between -2 and 2",
			}
		}
	}

	// Validate frequency_penalty if provided
	if req.FrequencyPenalty != nil {
		if *req.FrequencyPenalty < -2 || *req.FrequencyPenalty > 2 {
			return &ValidationError{
				Field:   "frequency_penalty",
				Message: "frequency_penalty must be between -2 and 2",
			}
		}
	}

	// Validate route if provided
	if req.Route != "" {
		validRoutes := map[string]bool{
			"fallback": true,
			"balanced": true,
			"priority": true,
			"random":   true,
		}

		if !validRoutes[req.Route] {
			return &ValidationError{
				Field:   "route",
				Message: fmt.Sprintf("invalid route '%s', must be one of: fallback, balanced, priority, random", req.Route),
			}
		}
	}

	return nil
}

// ValidateEmbeddingsRequest validates an embeddings request
func ValidateEmbeddingsRequest(req *EmbeddingsRequest) error {
	// Model is required
	if req.Model == "" {
		return &ValidationError{
			Field:   "model",
			Message: "model is required",
		}
	}

	// Input is required
	if req.Input == nil {
		return &ValidationError{
			Field:   "input",
			Message: "input is required",
		}
	}

	// Validate input type - should be string or []string
	switch v := req.Input.(type) {
	case string:
		if v == "" {
			return &ValidationError{
				Field:   "input",
				Message: "input string cannot be empty",
			}
		}
	case []string:
		if len(v) == 0 {
			return &ValidationError{
				Field:   "input",
				Message: "input array cannot be empty",
			}
		}
		for i, s := range v {
			if s == "" {
				return &ValidationError{
					Field:   fmt.Sprintf("input[%d]", i),
					Message: "input string cannot be empty",
				}
			}
		}
	case []interface{}:
		if len(v) == 0 {
			return &ValidationError{
				Field:   "input",
				Message: "input array cannot be empty",
			}
		}
		// Validate each element is a string
		for i, elem := range v {
			if s, ok := elem.(string); !ok || s == "" {
				return &ValidationError{
					Field:   fmt.Sprintf("input[%d]", i),
					Message: "input must be a non-empty string",
				}
			}
		}
	default:
		return &ValidationError{
			Field:   "input",
			Message: "input must be a string or array of strings",
		}
	}

	// Validate encoding_format if provided
	if req.EncodingFormat != "" {
		validFormats := map[string]bool{
			"float":  true,
			"base64": true,
		}

		if !validFormats[req.EncodingFormat] {
			return &ValidationError{
				Field:   "encoding_format",
				Message: fmt.Sprintf("invalid encoding_format '%s', must be one of: float, base64", req.EncodingFormat),
			}
		}
	}

	// Validate dimensions if provided
	if req.Dimensions != nil && *req.Dimensions < 1 {
		return &ValidationError{
			Field:   "dimensions",
			Message: "dimensions must be at least 1",
		}
	}

	return nil
}

// NormalizeModelID ensures the model ID has the correct format
func NormalizeModelID(modelID string) string {
	// If the model already has a provider prefix, return as-is
	if strings.Contains(modelID, "/") {
		return modelID
	}

	// Otherwise, we need to determine the provider from context
	// This will be handled by the routing logic
	return modelID
}

// ExtractProviderFromModel extracts the provider name from a model ID
func ExtractProviderFromModel(modelID string) (provider, model string) {
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	// No provider prefix
	return "", modelID
}
