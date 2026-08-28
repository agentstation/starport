package proxy

import (
	"fmt"
	"strings"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/inference"
)

// fieldMessages is the request field a refusal names when the fault is in the
// conversation the caller sent, whatever produced it: an empty array, a
// malformed part, or a document the gateway could not read.
const fieldMessages = "messages"

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
		return validationError(fieldMessages, "messages array cannot be empty")
	}
	if err := validateMessages(request.Messages); err != nil {
		return err
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

// validateMessages checks each message and each content part it carries. It is
// separate from the request rules above because a message is its own shape: a
// role, an optional tool link, and a content list whose parts each have their
// own rules.
func validateMessages(messages []inference.Message) error {
	for index, message := range messages {
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
			// A document names its bytes once. Each protocol codec refuses a
			// conflict at its own field path, and this check holds the same
			// rule for the canonical request every surface reaches the proxy
			// with, whether or not a codec built it.
			if part.Document != nil {
				if err := part.Document.Validate(); err != nil {
					return validationError(
						fmt.Sprintf("%s.content[%d].file", field, partIndex),
						err.Error(),
					)
				}
			}
		}
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

// ValidateRerankRequest checks one canonical rerank request. The codec already
// refuses an empty query and an empty document list, so this guard covers the
// caller the codec does not cover: a gateway-internal one that builds the
// request itself.
func ValidateRerankRequest(req *RerankRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	request := req.Request
	if request.Model == "" {
		return validationError("model", "model is required")
	}
	if request.Query == "" {
		return validationError("query", "query is required")
	}
	if len(request.Documents) == 0 {
		return validationError("documents", "documents is required")
	}
	for index, document := range request.Documents {
		if document == "" {
			return validationError(
				fmt.Sprintf("documents[%d]", index), "document cannot be empty",
			)
		}
	}
	// A result count of zero asks for a ranking the caller cannot read, and a
	// negative one asks for nothing at all. Both reach a provider as a paid
	// error, so they stop here.
	if request.TopN != nil && *request.TopN < 1 {
		return validationError("top_n", "top_n must be at least 1")
	}
	if request.MaxTokensPerDocument != nil && *request.MaxTokensPerDocument < 1 {
		return validationError("max_tokens_per_doc", "max_tokens_per_doc must be at least 1")
	}
	return nil
}

// ValidateImagesRequest checks one image generation or image edit request.
func ValidateImagesRequest(req *ImagesRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	if req.Request.Model == "" {
		return validationError("model", "model is required")
	}
	if req.Request.Prompt == "" {
		return validationError("prompt", "prompt is required")
	}
	if req.Request.N < 0 {
		return validationError("n", "n cannot be negative")
	}
	// A mask names a region of the source image, so it means nothing without
	// one. Accepting the pair silently would send a provider an edit it
	// cannot apply and charge the caller for the rejection.
	if req.Request.Mask.Present() && !req.Request.Image.Present() {
		return validationError("mask", "mask requires an image")
	}
	return nil
}

// ValidateVideoJobRequest checks one video generation submission.
func ValidateVideoJobRequest(req *VideoSubmitRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	if req.Request.Model == "" {
		return validationError("model", "model is required")
	}
	if req.Request.Prompt == "" {
		return validationError("prompt", "prompt is required")
	}
	return nil
}

// ValidateVideoJobReference checks one poll or cancel of an accepted job. A
// reference with no provider would plan a route to whichever provider the
// catalog ranked first, and that provider never issued the identifier.
func ValidateVideoJobReference(req *VideoJobRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	if req.Request.Model == "" {
		return validationError("model", "model is required")
	}
	if req.Request.Provider == "" {
		return validationError("provider", "provider is required")
	}
	if req.Request.ProviderJobID == "" {
		return validationError("job", "an accepted job is required")
	}
	return nil
}

// ValidateVideoAssetReference checks one read of a finished job's asset. It
// adds the stored bound to the reference checks: a read with no bound would let
// the provider's answer size this deployment's storage.
func ValidateVideoAssetReference(req *VideoAssetRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	if req.Request.Model == "" {
		return validationError("model", "model is required")
	}
	if req.Request.Provider == "" {
		return validationError("provider", "provider is required")
	}
	if req.Request.ProviderJobID == "" {
		return validationError("job", "an accepted job is required")
	}
	if req.Request.MaxBytes <= 0 {
		return validationError("bound", "a stored byte bound is required")
	}
	return nil
}

// ValidateSpeechRequest checks one text-to-speech request.
func ValidateSpeechRequest(req *SpeechRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	if req.Request.Model == "" {
		return validationError("model", "model is required")
	}
	if req.Request.Input == "" {
		return validationError("input", "input is required")
	}
	if req.Request.Speed != nil && *req.Request.Speed <= 0 {
		return validationError("speed", "speed must be greater than 0")
	}
	return nil
}

// ValidateTranscriptionRequest checks one speech-to-text request.
func ValidateTranscriptionRequest(req *TranscriptionRequest) error {
	if req == nil {
		return validationError("request", "request is required")
	}
	if req.Request.Model == "" {
		return validationError("model", "model is required")
	}
	if !req.Request.File.Present() {
		return validationError("file", "file is required")
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
