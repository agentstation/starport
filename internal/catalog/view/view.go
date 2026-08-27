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

	// Definition-level catalog facts
	Authors         []ModelAuthorInfo `json:"authors,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Lineage         *ModelLineageInfo `json:"lineage,omitempty"`
	KnowledgeCutoff string            `json:"knowledge_cutoff,omitempty"`
	OpenWeights     *bool             `json:"open_weights,omitempty"`

	// Offerings lists every routable provider offering for this model.
	Offerings []ModelOfferingInfo `json:"offerings,omitempty"`
}

// ModelAuthorInfo names one catalog author of a model.
type ModelAuthorInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ModelLineageInfo describes canonical model-family relationships.
type ModelLineageInfo struct {
	Family string `json:"family,omitempty"`
	Root   string `json:"root,omitempty"`
	Parent string `json:"parent,omitempty"`
}

// ModelOfferingInfo is one provider's routable offering of a model.
type ModelOfferingInfo struct {
	Provider            string               `json:"provider"`
	ProviderName        string               `json:"provider_name,omitempty"`
	ProviderModelID     string               `json:"provider_model_id"`
	ContextLength       *int                 `json:"context_length,omitempty"`
	MaxCompletionTokens *int                 `json:"max_completion_tokens,omitempty"`
	Availability        string               `json:"availability,omitempty"`
	Lifecycle           string               `json:"lifecycle,omitempty"`
	Pricing             *OfferingPricingInfo `json:"pricing,omitempty"`
	// Operations names what this offering serves, in the catalog's own
	// spelling. A media model reaches a different path than a chat model, so
	// a reader who cannot see the operations cannot tell which path to call.
	Operations []string `json:"operations,omitempty"`
}

// OfferingPricingInfo carries every token price dimension of one offering
// as decimal per-token strings.
type OfferingPricingInfo struct {
	Prompt     string `json:"prompt,omitempty"`
	Completion string `json:"completion,omitempty"`
	Reasoning  string `json:"reasoning,omitempty"`
	CacheRead  string `json:"cache_read,omitempty"`
	CacheWrite string `json:"cache_write,omitempty"`
	// Audio tokens bill at their own rate wherever a provider meters them,
	// so an audio turn cannot be priced from Prompt and Completion alone.
	AudioInput  string `json:"audio_input,omitempty"`
	AudioOutput string `json:"audio_output,omitempty"`
	Currency    string `json:"currency,omitempty"`
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
	DocsURL          string                `json:"docs_url,omitempty"`
	URL              string                `json:"url,omitempty"`
	Models           []string              `json:"models"`
	Capabilities     []string              `json:"capabilities,omitempty"`
	RequiresAuth     bool                  `json:"requires_auth"`
	AuthDescription  string                `json:"auth_description,omitempty"`
	CredentialFields []CredentialFieldInfo `json:"credential_fields,omitempty"`
	Headquarters     string                `json:"headquarters,omitempty"`
	Policies         *ProviderPolicyInfo   `json:"policies,omitempty"`
}

// ProviderPolicyInfo summarizes the provider's published data policies.
type ProviderPolicyInfo struct {
	PrivacyPolicyURL  string `json:"privacy_policy_url,omitempty"`
	TermsOfServiceURL string `json:"terms_of_service_url,omitempty"`
	RetainsData       *bool  `json:"retains_data,omitempty"`
	TrainsOnData      *bool  `json:"trains_on_data,omitempty"`
	Retention         string `json:"retention,omitempty"`
	Moderated         *bool  `json:"moderated,omitempty"`
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
