package connectors

import (
	"strings"

	"github.com/agentstation/starport/pkg/catalog"
)

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
	Prompt      string `json:"prompt"`                 // Price per 1k tokens
	Completion  string `json:"completion"`             // Price per 1k tokens
	Image       string `json:"image,omitempty"`        // Price per image
	Request     string `json:"request,omitempty"`      // Price per request
	CacheWrite  string `json:"cache_write,omitempty"`  // Price per 1k tokens for cache writes
	CacheRead   string `json:"cache_read,omitempty"`   // Price per 1k tokens for cache reads
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
			Slug:                  "google-ai-studio",
			MayLogPrompts:         false,
			MayTrainOnData:        false,
			ModeratedByOpenRouter: false,
			PrivacyPolicyURL:      "https://policies.google.com/privacy",
			TermsOfServiceURL:     "https://policies.google.com/terms",
			StatusPageURL:         "",
		},
		{
			Name:                  "Google Vertex AI",
			Slug:                  "google-vertex",
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
	// Get catalog
	catalog, err := catalog.GetCatalog()
	if err != nil {
		// Fall back to basic metadata if catalog fails to load
		return &ModelMetadata{
			ID:      modelID,
			Name:    modelID,
			Created: 1686935002, // Default timestamp
		}
	}

	// Look up model in catalog
	model := catalog.GetModelByID(modelID)
	if model == nil {
		// Return basic metadata if not found
		return &ModelMetadata{
			ID:      modelID,
			Name:    modelID,
			Created: 1686935002, // Default timestamp
		}
	}

	// Convert catalog model to metadata
	metadata := &ModelMetadata{
		ID:          model.ID,
		Name:        model.Name,
		Created:     model.Created,
		Description: model.Description,
	}

	// Convert pricing
	if model.Pricing != nil {
		metadata.Pricing = &ModelPricing{
			Prompt:     model.Pricing.Prompt,
			Completion: model.Pricing.Completion,
			Image:      model.Pricing.Image,
			Request:    model.Pricing.Request,
		}
	}

	// Convert context length
	if model.ContextLength > 0 {
		ctx := ModelContext(model.ContextLength)
		metadata.Context = &ctx
	}

	// Convert architecture
	if model.Architecture != nil {
		metadata.Architecture = &ModelArchitecture{
			InputModalities:  model.Architecture.InputModalities,
			OutputModalities: model.Architecture.OutputModalities,
			Tokenizer:        model.Architecture.Tokenizer,
		}
	}

	// Convert top provider
	if model.TopProvider != nil {
		metadata.TopProvider = &TopProviderInfo{
			IsModerated:         false, // Not in catalog, defaulting
			MaxCompletionTokens: model.TopProvider.MaxCompletionTokens,
		}
	}

	// Copy supported parameters
	metadata.SupportedParameters = model.SupportedParameters

	return metadata
}

// GetCachePricing returns cache pricing for a model/provider combination
func GetCachePricing(modelID string) *ModelPricing {
	// Extract provider from model ID
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) < 2 {
		return nil
	}
	provider := parts[0]
	
	// Cache pricing as of OpenRouter docs
	// Note: These are illustrative examples, actual pricing should be fetched from provider APIs
	cachePricing := map[string]map[string]*ModelPricing{
		"anthropic": {
			"claude-3-5-sonnet": {
				Prompt:     "0.003",     // $3 per 1M tokens
				Completion: "0.015",     // $15 per 1M tokens
				CacheWrite: "0.00375",   // $3.75 per 1M tokens (1.25x prompt)
				CacheRead:  "0.0003",    // $0.30 per 1M tokens (1/10 prompt)
			},
			"claude-3-haiku": {
				Prompt:     "0.00025",   // $0.25 per 1M tokens
				Completion: "0.00125",   // $1.25 per 1M tokens
				CacheWrite: "0.0003",    // $0.30 per 1M tokens
				CacheRead:  "0.00003",   // $0.03 per 1M tokens
			},
		},
		"openai": {
			"gpt-4o": {
				Prompt:     "0.0025",    // $2.50 per 1M tokens
				Completion: "0.01",      // $10 per 1M tokens  
				CacheWrite: "0.00625",   // $6.25 per 1M tokens (2.5x prompt)
				CacheRead:  "0.00125",   // $1.25 per 1M tokens (0.5x prompt)
			},
			"gpt-4o-mini": {
				Prompt:     "0.00015",   // $0.15 per 1M tokens
				Completion: "0.0006",    // $0.60 per 1M tokens
				CacheWrite: "0.000375",  // $0.375 per 1M tokens
				CacheRead:  "0.000075",  // $0.075 per 1M tokens
			},
		},
		"deepseek": {
			"deepseek-chat": {
				Prompt:     "0.00014",   // $0.14 per 1M tokens
				Completion: "0.00028",   // $0.28 per 1M tokens
				CacheWrite: "0.00014",   // Same as prompt (free caching)
				CacheRead:  "0.000014",  // $0.014 per 1M tokens (0.1x prompt)
			},
		},
	}
	
	// Look up cache pricing
	if providerPricing, ok := cachePricing[provider]; ok {
		// Try exact model match
		if pricing, ok := providerPricing[parts[1]]; ok {
			return pricing
		}
		// Try without version suffix (e.g., "claude-3-5-sonnet-20241022" -> "claude-3-5-sonnet")
		baseName := strings.Split(parts[1], "-20")[0] // Simple heuristic for removing date suffixes
		if pricing, ok := providerPricing[baseName]; ok {
			return pricing
		}
	}
	
	return nil
}


// GetModelsByProvider returns models offered by a specific provider
func GetModelsByProvider(provider string) []string {
	// Get catalog
	cat, err := catalog.GetCatalog()
	if err != nil {
		return []string{}
	}

	// Get models from catalog
	catalogModels := cat.GetModelsByProvider(provider)
	
	// Convert to string slice
	models := make([]string, 0, len(catalogModels))
	for _, model := range catalogModels {
		models = append(models, model.ID)
	}
	
	return models
}

// GetProvidersForModel returns providers that offer a specific model
func GetProvidersForModel(modelName string) []ModelProviderInfo {
	providers := []ModelProviderInfo{}

	// Get catalog
	cat, err := catalog.GetCatalog()
	if err != nil {
		return providers
	}

	// Search for models with the given name across all providers
	for modelID, model := range cat.Models {
		parts := strings.SplitN(modelID, "/", 2)
		if len(parts) == 2 && parts[1] == modelName {
			providerSlug := parts[0]
			providerMeta := getProviderBySlug(providerSlug)
			if providerMeta != nil {
				info := ModelProviderInfo{
					Provider: providerSlug,
					Name:     providerMeta.Name,
				}
				
				// Convert pricing
				if model.Pricing != nil {
					info.Pricing = &ModelPricing{
						Prompt:     model.Pricing.Prompt,
						Completion: model.Pricing.Completion,
						Image:      model.Pricing.Image,
						Request:    model.Pricing.Request,
					}
				}
				
				// Convert context
				if model.ContextLength > 0 {
					ctx := ModelContext(model.ContextLength)
					info.Context = &ctx
				}
				
				providers = append(providers, info)
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