// Package catalog provides types and utilities for working with model catalogs
package catalog

import "encoding/json"

// Catalog represents the complete parsed catalog with multiple indexes for O(1) lookups
type Catalog struct {
	Models                   map[string]*Model      `json:"models"`
	Providers                map[string]*Provider   `json:"providers"`
	EndpointsByModel         map[string][]*Endpoint `json:"endpoints_by_model"`
	EndpointsByProviderModel map[string][]*Endpoint `json:"endpoints_by_provider_model"`
	EndpointsByTag           map[string][]*Endpoint `json:"endpoints_by_tag"`
}

// Model represents a model with OpenRouter-compatible fields
type Model struct {
	ID                  string           `json:"id"`
	CanonicalSlug       string           `json:"canonical_slug,omitempty"`
	Name                string           `json:"name"`
	Created             int64            `json:"created,omitempty"`
	Description         string           `json:"description,omitempty"`
	ContextLength       int              `json:"context_length"`
	Architecture        *Architecture    `json:"architecture,omitempty"`
	Pricing             *Pricing         `json:"pricing,omitempty"`
	TopProvider         *TopProvider     `json:"top_provider,omitempty"`
	SupportedParameters []string         `json:"supported_parameters,omitempty"`
	HuggingFaceID       string           `json:"hugging_face_id,omitempty"`
}

// Provider represents a provider with OpenRouter-compatible fields
type Provider struct {
	Name                  string  `json:"name"`
	Slug                  string  `json:"slug"`
	PrivacyPolicyURL      *string `json:"privacy_policy_url,omitempty"`
	TermsOfServiceURL     *string `json:"terms_of_service_url,omitempty"`
	StatusPageURL         *string `json:"status_page_url,omitempty"`
	MayLogPrompts         bool    `json:"may_log_prompts"`
	MayTrainOnData        bool    `json:"may_train_on_data"`
	ModeratedByOpenRouter bool    `json:"moderated_by_openrouter"`
}

// Endpoint represents an endpoint with provider context
type Endpoint struct {
	Name                string           `json:"name"`
	ProviderName        string           `json:"provider_name"`
	ProviderSlug        string           `json:"provider_slug"`
	Tag                 string           `json:"tag,omitempty"`
	ContextLength       int              `json:"context_length"`
	Pricing             *EndpointPricing `json:"pricing,omitempty"`
	SupportedParameters []string         `json:"supported_parameters,omitempty"`
	MaxCompletionTokens int              `json:"max_completion_tokens,omitempty"`
	Quantization        string           `json:"quantization,omitempty"`
	Status              int              `json:"status,omitempty"`
	UptimeLast30m       float64          `json:"uptime_last_30m,omitempty"`
}

// Architecture represents model architecture information
type Architecture struct {
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	Modality         string   `json:"modality,omitempty"`
	Tokenizer        string   `json:"tokenizer,omitempty"`
	InstructType     string   `json:"instruct_type,omitempty"`
}

// Pricing represents model pricing information
type Pricing struct {
	Prompt            string `json:"prompt,omitempty"`
	Completion        string `json:"completion,omitempty"`
	Request           string `json:"request,omitempty"`
	Image             string `json:"image,omitempty"`
	WebSearch         string `json:"web_search,omitempty"`
	InternalReasoning string `json:"internal_reasoning,omitempty"`
}

// EndpointPricing represents endpoint-specific pricing
type EndpointPricing struct {
	Prompt            string `json:"prompt,omitempty"`
	Completion        string `json:"completion,omitempty"`
	Request           string `json:"request,omitempty"`
	Image             string `json:"image,omitempty"`
	WebSearch         string `json:"web_search,omitempty"`
	InternalReasoning string `json:"internal_reasoning,omitempty"`
	InputCacheRead    string `json:"input_cache_read,omitempty"`
	InputCacheWrite   string `json:"input_cache_write,omitempty"`
}

// TopProvider represents top provider information
type TopProvider struct {
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
	ContextLength       int `json:"context_length,omitempty"`
}

// Raw data structures for loading

// ProvidersData wraps the providers array from providers.json
type ProvidersData struct {
	Data []json.RawMessage `json:"data"`
}

// ModelsData wraps the models array from models.json
type ModelsData struct {
	Data []json.RawMessage `json:"data"`
}

// EndpointsData represents the endpoints.json structure
type EndpointsData struct {
	Models []ModelEndpoints `json:"models"`
}

// ModelEndpoints represents a model with its endpoints from endpoints.json
type ModelEndpoints struct {
	ID           string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	Created      int64             `json:"created,omitempty"`
	Description  string            `json:"description,omitempty"`
	Architecture *Architecture     `json:"architecture,omitempty"`
	Endpoints    []json.RawMessage `json:"endpoints"`
	FetchedAt    string            `json:"fetched_at,omitempty"`
}