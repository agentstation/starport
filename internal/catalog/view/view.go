// Package view owns the console- and API-facing projections of one
// routable catalog snapshot. It assembles model, provider, and endpoint
// DTOs from Starmap facts; transports serialize these shapes without
// re-deriving catalog data.
package view

// ModelInfo represents model information
type ModelInfo struct {
	ID            string `json:"id"`
	CanonicalSlug string `json:"canonical_slug,omitempty"`
	Name          string `json:"name,omitempty"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`

	// Extended metadata for OpenRouter compatibility
	Pricing             *ModelPricing      `json:"pricing,omitempty"`
	Context             *int               `json:"context_length,omitempty"`
	Type                string             `json:"type,omitempty"`
	Description         string             `json:"description,omitempty"`
	Architecture        *ModelArchitecture `json:"architecture,omitempty"`
	TopProvider         *TopProviderInfo   `json:"top_provider,omitempty"`
	SupportedParameters []string           `json:"supported_parameters,omitempty"`
}

// ModelPricing represents model pricing information
type ModelPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
	Currency   string `json:"currency"`
}

// ModelArchitecture describes protocol-facing model capabilities.
type ModelArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Tokenizer        string   `json:"tokenizer"`
	InstructType     *string  `json:"instruct_type"`
}

// TopProviderInfo describes the selected representative offering limits.
type TopProviderInfo struct {
	ContextLength       int `json:"context_length"`
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

// ProviderInfo represents provider metadata
type ProviderInfo struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	URL              string                `json:"url,omitempty"`
	Models           []string              `json:"models"`
	Capabilities     []string              `json:"capabilities,omitempty"`
	RequiresAuth     bool                  `json:"requires_auth"`
	AuthDescription  string                `json:"auth_description,omitempty"`
	CredentialFields []CredentialFieldInfo `json:"credential_fields,omitempty"`
}

// CredentialFieldInfo is the catalog-declared inference credential field a
// caller supplies for BYOK. It carries no secret values.
type CredentialFieldInfo struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// EndpointInfo represents an endpoint that can serve a model
type EndpointInfo struct {
	Provider   string `json:"provider"`
	Endpoint   string `json:"endpoint"`
	Available  bool   `json:"available"`
	Latency    *int   `json:"latency_ms,omitempty"`
	CostPrompt string `json:"cost_prompt,omitempty"`
	CostOutput string `json:"cost_output,omitempty"`
}
