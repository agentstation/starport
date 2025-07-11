package connectors

import "strings"

// ModelMetadata represents enhanced model information for OpenRouter compatibility
type ModelMetadata struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	Created             int64              `json:"created"`
	Description         string             `json:"description,omitempty"`
	Pricing             *ModelPricing      `json:"pricing,omitempty"`
	Context             *ModelContext      `json:"context_length,omitempty"`
	Architecture        *ModelArchitecture `json:"architecture,omitempty"`
	TopProvider         *TopProviderInfo   `json:"top_provider,omitempty"`
	SupportedParameters []string           `json:"supported_parameters,omitempty"`
}

// ModelPricing represents pricing information
type ModelPricing struct {
	Prompt     string `json:"prompt"`            // Price per 1k tokens
	Completion string `json:"completion"`        // Price per 1k tokens
	Image      string `json:"image,omitempty"`   // Price per image
	Request    string `json:"request,omitempty"` // Price per request
}

// ModelContext represents context window information
type ModelContext int

// ModelArchitecture represents model architecture details
type ModelArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Tokenizer        string   `json:"tokenizer"`
}

// TopProviderInfo represents the top provider information
type TopProviderInfo struct {
	IsModerated         bool `json:"is_moderated"`
	MaxCompletionTokens int  `json:"max_completion_tokens,omitempty"`
}

// ProviderMetadata represents enhanced provider information (OpenRouter-compatible)
type ProviderMetadata struct {
	Name                  string `json:"name"`
	Slug                  string `json:"slug"`
	MayLogPrompts         bool   `json:"may_log_prompts"`
	MayTrainOnData        bool   `json:"may_train_on_data"`
	ModeratedByOpenRouter bool   `json:"moderated_by_openrouter"`
	PrivacyPolicyURL      string `json:"privacy_policy_url"`
	TermsOfServiceURL     string `json:"terms_of_service_url,omitempty"`
	StatusPageURL         string `json:"status_page_url,omitempty"`
}

// EnhancedModelsResponse represents the enhanced models response
type EnhancedModelsResponse struct {
	Object string          `json:"object"`
	Data   []ModelMetadata `json:"data"`
}

// ProvidersResponse represents the providers list response
type ProvidersResponse struct {
	Data []ProviderMetadata `json:"data"`
}

// ModelProvidersResponse represents providers that offer a specific model
type ModelProvidersResponse struct {
	Model     string              `json:"model"`
	Providers []ModelProviderInfo `json:"providers"`
}

// ModelProviderInfo represents information about a provider offering a model
type ModelProviderInfo struct {
	Provider string        `json:"provider"`
	Name     string        `json:"name"`
	Pricing  *ModelPricing `json:"pricing,omitempty"`
	Context  *ModelContext `json:"context_length,omitempty"`
}

// GetProviderMetadata returns metadata for all supported providers
func GetProviderMetadata() []ProviderMetadata {
	return []ProviderMetadata{
		{
			Name:                  "OpenAI",
			Slug:                  "openai",
			MayLogPrompts:         false,
			MayTrainOnData:        false,
			ModeratedByOpenRouter: false,
			PrivacyPolicyURL:      "https://openai.com/privacy",
			TermsOfServiceURL:     "https://openai.com/terms",
			StatusPageURL:         "https://status.openai.com",
		},
		{
			Name:                  "Anthropic",
			Slug:                  "anthropic",
			MayLogPrompts:         false,
			MayTrainOnData:        false,
			ModeratedByOpenRouter: false,
			PrivacyPolicyURL:      "https://www.anthropic.com/privacy",
			TermsOfServiceURL:     "https://www.anthropic.com/terms",
			StatusPageURL:         "https://status.anthropic.com",
		},
		{
			Name:                  "Google AI Studio",
			Slug:                  "google-aistudio",
			MayLogPrompts:         false,
			MayTrainOnData:        false,
			ModeratedByOpenRouter: false,
			PrivacyPolicyURL:      "https://policies.google.com/privacy",
			TermsOfServiceURL:     "https://policies.google.com/terms",
			StatusPageURL:         "",
		},
		{
			Name:                  "Google Vertex AI",
			Slug:                  "google-vertexai",
			MayLogPrompts:         false,
			MayTrainOnData:        false,
			ModeratedByOpenRouter: false,
			PrivacyPolicyURL:      "https://cloud.google.com/terms/cloud-privacy-notice",
			TermsOfServiceURL:     "https://cloud.google.com/terms",
			StatusPageURL:         "https://status.cloud.google.com",
		},
		{
			Name:                  "Groq",
			Slug:                  "groq",
			MayLogPrompts:         false,
			MayTrainOnData:        false,
			ModeratedByOpenRouter: false,
			PrivacyPolicyURL:      "https://groq.com/privacy-policy",
			TermsOfServiceURL:     "https://groq.com/terms",
			StatusPageURL:         "",
		},
		{
			Name:                  "Mistral AI",
			Slug:                  "mistral",
			MayLogPrompts:         false,
			MayTrainOnData:        false,
			ModeratedByOpenRouter: false,
			PrivacyPolicyURL:      "https://mistral.ai/terms",
			TermsOfServiceURL:     "https://mistral.ai/terms",
			StatusPageURL:         "",
		},
		{
			Name:                  "Azure OpenAI",
			Slug:                  "azure",
			MayLogPrompts:         false,
			MayTrainOnData:        false,
			ModeratedByOpenRouter: false,
			PrivacyPolicyURL:      "https://privacy.microsoft.com",
			TermsOfServiceURL:     "https://azure.microsoft.com/support/legal",
			StatusPageURL:         "https://azure.microsoft.com/status",
		},
	}
}

// GetModelMetadata returns enhanced metadata for a model
func GetModelMetadata(modelID string) *ModelMetadata {
	// This will be populated with actual model metadata
	// For now, return a basic structure that can be enhanced
	metadata := modelMetadataMap[modelID]
	if metadata != nil {
		return metadata
	}

	// Return basic metadata if not found in map
	return &ModelMetadata{
		ID:      modelID,
		Name:    modelID,
		Created: 1686935002, // Default timestamp
	}
}

// modelMetadataMap contains enhanced metadata for known models
var modelMetadataMap = map[string]*ModelMetadata{
	// OpenAI Models
	"openai/gpt-4": {
		ID:          "openai/gpt-4",
		Name:        "OpenAI: GPT-4",
		Created:     1686935002,
		Description: "GPT-4 is OpenAI's latest and most powerful model",
		Pricing: &ModelPricing{
			Prompt:     "0.00003",
			Completion: "0.00006",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(8192),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "cl100k_base",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "frequency_penalty",
			"presence_penalty", "tools", "tool_choice",
			"response_format", "seed", "max_tokens",
		},
	},
	"openai/gpt-4-turbo": {
		ID:          "openai/gpt-4-turbo",
		Name:        "OpenAI: GPT-4 Turbo",
		Created:     1699593002,
		Description: "GPT-4 Turbo with 128k context window",
		Pricing: &ModelPricing{
			Prompt:     "0.00001",
			Completion: "0.00003",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(128000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "cl100k_base",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "frequency_penalty",
			"presence_penalty", "tools", "tool_choice",
			"response_format", "seed", "max_tokens",
		},
	},
	"openai/gpt-3.5-turbo": {
		ID:          "openai/gpt-3.5-turbo",
		Name:        "OpenAI: GPT-3.5 Turbo",
		Created:     1677649963,
		Description: "Fast and efficient model for most tasks",
		Pricing: &ModelPricing{
			Prompt:     "0.0000005",
			Completion: "0.0000015",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(16385),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "cl100k_base",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "frequency_penalty",
			"presence_penalty", "tools", "tool_choice",
			"response_format", "seed", "max_tokens",
		},
	},

	// Anthropic Models - Claude 4 Series
	"anthropic/claude-4-opus": {
		ID:          "anthropic/claude-4-opus",
		Name:        "Anthropic: Claude 4 Opus",
		Created:     1736524800, // Jan 2025
		Description: "Claude 4 Opus - Most advanced model with superior reasoning",
		Pricing: &ModelPricing{
			Prompt:     "0.000015",
			Completion: "0.000075",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(200000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "claude",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop_sequences", "stream",
		},
	},
	"anthropic/claude-4-sonnet": {
		ID:          "anthropic/claude-4-sonnet",
		Name:        "Anthropic: Claude 4 Sonnet",
		Created:     1736524800, // Jan 2025
		Description: "Claude 4 Sonnet - Balanced performance and cost",
		Pricing: &ModelPricing{
			Prompt:     "0.000006",
			Completion: "0.00003",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(200000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "claude",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop_sequences", "stream",
		},
	},

	// Anthropic Models - Claude 3 Series
	"anthropic/claude-3-opus-20240229": {
		ID:          "anthropic/claude-3-opus-20240229",
		Name:        "Anthropic: Claude 3 Opus",
		Created:     1709251200,
		Description: "Claude 3 Opus - Most powerful model for complex tasks",
		Pricing: &ModelPricing{
			Prompt:     "0.000015",
			Completion: "0.000075",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(200000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "claude",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop_sequences", "stream",
		},
	},
	"anthropic/claude-3-sonnet-20240229": {
		ID:          "anthropic/claude-3-sonnet-20240229",
		Name:        "Anthropic: Claude 3 Sonnet",
		Created:     1709251200,
		Description: "Claude 3 Sonnet - Balanced performance and cost",
		Pricing: &ModelPricing{
			Prompt:     "0.000003",
			Completion: "0.000015",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(200000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "claude",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop_sequences", "stream",
		},
	},
	"anthropic/claude-3-haiku-20240307": {
		ID:          "anthropic/claude-3-haiku-20240307",
		Name:        "Anthropic: Claude 3 Haiku",
		Created:     1709856000,
		Description: "Claude 3 Haiku - Fast and cost-effective",
		Pricing: &ModelPricing{
			Prompt:     "0.00000025",
			Completion: "0.00000125",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(200000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "claude",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop_sequences", "stream",
		},
	},

	// Google AI Studio Models
	"google-aistudio/gemini-1.5-pro": {
		ID:          "google-aistudio/gemini-1.5-pro",
		Name:        "Google AI Studio: Gemini 1.5 Pro",
		Created:     1707868800,
		Description: "Gemini 1.5 Pro with 1M+ context window",
		Pricing: &ModelPricing{
			Prompt:     "0.0000035",
			Completion: "0.0000105",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(1048576),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image", "video", "audio"},
			OutputModalities: []string{"text"},
			Tokenizer:        "gemini",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 8192,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "top_k", "max_tokens",
			"stop_sequences", "candidate_count",
		},
	},
	"google-aistudio/gemini-1.5-flash": {
		ID:          "google-aistudio/gemini-1.5-flash",
		Name:        "Google AI Studio: Gemini 1.5 Flash",
		Created:     1715299200,
		Description: "Fast multimodal model optimized for speed",
		Pricing: &ModelPricing{
			Prompt:     "0.00000035",
			Completion: "0.00000105",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(1048576),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image", "video", "audio"},
			OutputModalities: []string{"text"},
			Tokenizer:        "gemini",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 8192,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "top_k", "max_tokens",
			"stop_sequences", "candidate_count",
		},
	},

	// Google Vertex AI Models
	"google-vertexai/gemini-1.5-pro": {
		ID:          "google-vertexai/gemini-1.5-pro",
		Name:        "Google Vertex AI: Gemini 1.5 Pro",
		Created:     1707868800,
		Description: "Gemini 1.5 Pro on Vertex AI with 1M+ context window",
		Pricing: &ModelPricing{
			Prompt:     "0.00000125",
			Completion: "0.00000375",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(1048576),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image", "video", "audio"},
			OutputModalities: []string{"text"},
			Tokenizer:        "gemini",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 8192,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "top_k", "max_tokens",
			"stop_sequences", "candidate_count",
		},
	},
	"google-vertexai/gemini-1.5-flash": {
		ID:          "google-vertexai/gemini-1.5-flash",
		Name:        "Google Vertex AI: Gemini 1.5 Flash",
		Created:     1715299200,
		Description: "Fast multimodal model on Vertex AI optimized for speed",
		Pricing: &ModelPricing{
			Prompt:     "0.000000125",
			Completion: "0.000000375",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(1048576),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image", "video", "audio"},
			OutputModalities: []string{"text"},
			Tokenizer:        "gemini",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 8192,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "top_k", "max_tokens",
			"stop_sequences", "candidate_count",
		},
	},
	"google-vertexai/claude-3-opus@20240229": {
		ID:          "google-vertexai/claude-3-opus@20240229",
		Name:        "Google Vertex AI: Claude 3 Opus",
		Created:     1709251200,
		Description: "Claude 3 Opus available through Vertex AI Model Garden",
		Pricing: &ModelPricing{
			Prompt:     "0.000015",
			Completion: "0.000075",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(200000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "claude",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop_sequences", "stream",
		},
	},
	"google-vertexai/claude-3-sonnet@20240229": {
		ID:          "google-vertexai/claude-3-sonnet@20240229",
		Name:        "Google Vertex AI: Claude 3 Sonnet",
		Created:     1709251200,
		Description: "Claude 3 Sonnet available through Vertex AI Model Garden",
		Pricing: &ModelPricing{
			Prompt:     "0.000003",
			Completion: "0.000015",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(200000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "claude",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop_sequences", "stream",
		},
	},
	"google-vertexai/claude-3-haiku@20240307": {
		ID:          "google-vertexai/claude-3-haiku@20240307",
		Name:        "Google Vertex AI: Claude 3 Haiku",
		Created:     1709856000,
		Description: "Claude 3 Haiku available through Vertex AI Model Garden",
		Pricing: &ModelPricing{
			Prompt:     "0.00000025",
			Completion: "0.00000125",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(200000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "claude",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop_sequences", "stream",
		},
	},
	"google-vertexai/text-bison@001": {
		ID:          "google-vertexai/text-bison@001",
		Name:        "Google Vertex AI: PaLM 2 for Text",
		Created:     1683590400,
		Description: "PaLM 2 text generation model",
		Pricing: &ModelPricing{
			Prompt:     "0.000000125",
			Completion: "0.000000125",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(8192),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "palm",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 1024,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "top_k", "max_tokens",
			"stop_sequences",
		},
	},
	"google-vertexai/code-bison@001": {
		ID:          "google-vertexai/code-bison@001",
		Name:        "Google Vertex AI: Codey for Code Generation",
		Created:     1683590400,
		Description: "Codey model for code generation",
		Pricing: &ModelPricing{
			Prompt:     "0.000000125",
			Completion: "0.000000125",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(6144),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "palm",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 1024,
		},
		SupportedParameters: []string{
			"temperature", "max_tokens", "stop_sequences",
		},
	},

	// Groq Models
	"groq/llama-3.1-70b-versatile": {
		ID:          "groq/llama-3.1-70b-versatile",
		Name:        "Groq: Llama 3.1 70B",
		Created:     1721692800,
		Description: "Fast inference of Llama 3.1 70B on Groq LPU",
		Pricing: &ModelPricing{
			Prompt:     "0.00000059",
			Completion: "0.00000079",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(131072),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "llama3",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 8192,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"frequency_penalty", "presence_penalty",
			"stop", "stream",
		},
	},
	"groq/mixtral-8x7b-32768": {
		ID:          "groq/mixtral-8x7b-32768",
		Name:        "Groq: Mixtral 8x7B",
		Created:     1702425600,
		Description: "Fast Mixtral 8x7B inference on Groq",
		Pricing: &ModelPricing{
			Prompt:     "0.00000027",
			Completion: "0.00000027",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(32768),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "mistral",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 8192,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"frequency_penalty", "presence_penalty",
			"stop", "stream",
		},
	},

	// Mistral Models
	"mistral/mistral-large-latest": {
		ID:          "mistral/mistral-large-latest",
		Name:        "Mistral: Large",
		Created:     1716336000,
		Description: "Mistral's most capable model",
		Pricing: &ModelPricing{
			Prompt:     "0.000002",
			Completion: "0.000006",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(128000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "mistral",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 8192,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"tools", "tool_choice", "response_format",
			"stop", "stream",
		},
	},
	"mistral/mistral-medium-latest": {
		ID:          "mistral/mistral-medium-latest",
		Name:        "Mistral: Medium",
		Created:     1704412800,
		Description: "Balanced performance Mistral model",
		Pricing: &ModelPricing{
			Prompt:     "0.0000027",
			Completion: "0.0000081",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(32768),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "mistral",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 8192,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "max_tokens",
			"stop", "stream",
		},
	},

	// Azure Models (mirrors of OpenAI models)
	"azure/gpt-4": {
		ID:          "azure/gpt-4",
		Name:        "Azure: GPT-4",
		Created:     1686935002,
		Description: "GPT-4 on Azure OpenAI Service",
		Pricing: &ModelPricing{
			Prompt:     "0.00003",
			Completion: "0.00006",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(8192),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "cl100k_base",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "frequency_penalty",
			"presence_penalty", "tools", "tool_choice",
			"response_format", "seed", "max_tokens",
		},
	},
	"azure/gpt-4-turbo": {
		ID:          "azure/gpt-4-turbo",
		Name:        "Azure: GPT-4 Turbo",
		Created:     1699593002,
		Description: "GPT-4 Turbo on Azure OpenAI Service",
		Pricing: &ModelPricing{
			Prompt:     "0.00001",
			Completion: "0.00003",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(128000),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:        "cl100k_base",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "frequency_penalty",
			"presence_penalty", "tools", "tool_choice",
			"response_format", "seed", "max_tokens",
		},
	},
	"azure/gpt-3.5-turbo": {
		ID:          "azure/gpt-3.5-turbo",
		Name:        "Azure: GPT-3.5 Turbo",
		Created:     1677649963,
		Description: "GPT-3.5 Turbo on Azure OpenAI Service",
		Pricing: &ModelPricing{
			Prompt:     "0.0000005",
			Completion: "0.0000015",
			Image:      "0",
			Request:    "0",
		},
		Context: modelContextPtr(16385),
		Architecture: &ModelArchitecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "cl100k_base",
		},
		TopProvider: &TopProviderInfo{
			IsModerated:         false,
			MaxCompletionTokens: 4096,
		},
		SupportedParameters: []string{
			"temperature", "top_p", "frequency_penalty",
			"presence_penalty", "tools", "tool_choice",
			"response_format", "seed", "max_tokens",
		},
	},
}

// Helper function to get pointer to ModelContext
func modelContextPtr(i int) *ModelContext {
	ctx := ModelContext(i)
	return &ctx
}

// GetModelsByProvider returns models offered by a specific provider
func GetModelsByProvider(provider string) []string {
	models := []string{}
	for modelID := range modelMetadataMap {
		if strings.HasPrefix(modelID, provider+"/") {
			models = append(models, modelID)
		}
	}
	return models
}

// GetProvidersForModel returns providers that offer a specific model
func GetProvidersForModel(modelName string) []ModelProviderInfo {
	providers := []ModelProviderInfo{}

	// For now, we only have one provider per model
	// This would be enhanced to support multiple providers per model base
	for modelID, metadata := range modelMetadataMap {
		parts := strings.SplitN(modelID, "/", 2)
		if len(parts) == 2 && parts[1] == modelName {
			providerSlug := parts[0]
			providerMeta := getProviderBySlug(providerSlug)
			if providerMeta != nil {
				providers = append(providers, ModelProviderInfo{
					Provider: providerSlug,
					Name:     providerMeta.Name,
					Pricing:  metadata.Pricing,
					Context:  metadata.Context,
				})
			}
		}
	}

	return providers
}

// getProviderBySlug returns provider metadata by slug
func getProviderBySlug(slug string) *ProviderMetadata {
	providers := GetProviderMetadata()
	for _, p := range providers {
		if p.Slug == slug {
			return &p
		}
	}
	return nil
}
