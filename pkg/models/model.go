package models

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Architecture describes the model's technical capabilities (OpenRouter compatible)
type Architecture struct {
	InputModalities  []string `json:"input_modalities"`            // e.g., ["text", "image"]
	OutputModalities []string `json:"output_modalities"`           // e.g., ["text"]
	Tokenizer        string   `json:"tokenizer"`                   // e.g., "GPT", "Claude"
	InstructType     string   `json:"instruct_type,omitempty"`     // e.g., "alpaca", "chatml"
}

// Pricing represents cost structure (OpenRouter compatible)
type Pricing struct {
	Prompt              string `json:"prompt"`                          // Cost per input token as string
	Completion          string `json:"completion"`                      // Cost per output token as string
	Image               string `json:"image,omitempty"`                 // Cost per image
	Request             string `json:"request,omitempty"`               // Fixed cost per request
	WebSearch           string `json:"web_search,omitempty"`            // Cost per web search
	InternalReasoning   string `json:"internal_reasoning,omitempty"`    // Cost for reasoning tokens
	InputCacheRead      string `json:"input_cache_read,omitempty"`      // Cost for cache reads
	InputCacheWrite     string `json:"input_cache_write,omitempty"`     // Cost for cache writes
}

// TopProvider represents provider-specific configuration (OpenRouter compatible)
type TopProvider struct {
	IsModerated         bool  `json:"is_moderated"`                    // Content moderation enabled
	ContextLength       int64 `json:"context_length"`                  // Provider-specific context limit
	MaxCompletionTokens int64 `json:"max_completion_tokens,omitempty"` // Max tokens in response
}

// Model represents an AI model with OpenRouter-compatible structure
type Model struct {
	// Core identification
	ID      string `json:"id"`       // Model identifier (e.g., "gpt-4", "anthropic/claude-3-opus")
	Created int64  `json:"created"`  // Unix timestamp

	// OpenRouter core fields
	Name          string `json:"name"`                      // Human-friendly display name
	CanonicalSlug string `json:"canonical_slug,omitempty"`  // Permanent identifier
	Description   string `json:"description,omitempty"`     // Model capabilities description

	// Capabilities
	ContextLength   int64         `json:"context_length"`            // Maximum context window in tokens
	Architecture    *Architecture `json:"architecture,omitempty"`    // Technical capabilities
	HuggingFaceID   string        `json:"hugging_face_id,omitempty"` // HuggingFace model ID if applicable

	// Pricing
	Pricing *Pricing `json:"pricing,omitempty"` // Cost structure

	// Provider details
	TopProvider *TopProvider `json:"top_provider,omitempty"` // Provider configuration

	// Parameters and limits
	SupportedParameters []string               `json:"supported_parameters,omitempty"` // e.g., ["temperature", "top_p"]
	PerRequestLimits    map[string]interface{} `json:"per_request_limits,omitempty"`   // Rate limits

	// Internal fields (not in API responses)
	Provider         string    `json:"-"` // Parent provider ID for internal use
	Deprecated       bool      `json:"-"` // Model is deprecated
	DeprecatedAt     time.Time `json:"-"` // When it was deprecated
	ReplacedBy       string    `json:"-"` // Suggested replacement model ID
	UpdatedAt        time.Time `json:"-"` // Last update time
	
	// Additional internal fields (not in API responses)
	Tags []string `json:"-"` // Tags for categorization
}

// IsChat returns true if this is a chat model
func (m *Model) IsChat() bool {
	return m.hasModalities([]string{"text"}, []string{"text"}) ||
		m.hasModalities([]string{"text", "image"}, []string{"text"})
}

// IsEmbedding returns true if this is an embedding model
func (m *Model) IsEmbedding() bool {
	return m.hasModalities([]string{"text"}, []string{"embedding"})
}

// IsImage returns true if this is an image model
func (m *Model) IsImage() bool {
	return m.hasModalities([]string{"text"}, []string{"image"})
}

// IsAudio returns true if this is an audio model
func (m *Model) IsAudio() bool {
	return m.hasModalities([]string{"audio"}, []string{"text"})
}

// IsModeration returns true if this is a moderation model
func (m *Model) IsModeration() bool {
	return m.hasModalities([]string{"text"}, []string{"classification"})
}

// hasModalities checks if the model has the specified input and output modalities
func (m *Model) hasModalities(expectedInput, expectedOutput []string) bool {
	if m.Architecture == nil {
		return false
	}
	
	// Check if all expected inputs are present
	for _, expected := range expectedInput {
		found := false
		for _, actual := range m.Architecture.InputModalities {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	
	// Check if all expected outputs are present
	for _, expected := range expectedOutput {
		found := false
		for _, actual := range m.Architecture.OutputModalities {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	
	return true
}

// IsActive returns true if the model is not deprecated
func (m *Model) IsActive() bool {
	return !m.Deprecated
}

// HasFeature checks if the model supports a specific feature
func (m *Model) HasFeature(feature string) bool {
	for _, p := range m.SupportedParameters {
		if p == feature {
			return true
		}
	}
	return false
}

// GetInputCost returns the input cost per 1K tokens (for backward compatibility)
// Note: OpenRouter stores pricing per token, this method converts to per 1K tokens
func (m *Model) GetInputCost() float64 {
	if m.Pricing == nil || m.Pricing.Prompt == "" {
		return 0
	}
	cost, err := strconv.ParseFloat(m.Pricing.Prompt, 64)
	if err != nil {
		return 0
	}
	return cost * 1000 // Convert from per-token to per-1K-tokens
}

// GetOutputCost returns the output cost per 1K tokens (for backward compatibility)
// Note: OpenRouter stores pricing per token, this method converts to per 1K tokens
func (m *Model) GetOutputCost() float64 {
	if m.Pricing == nil || m.Pricing.Completion == "" {
		return 0
	}
	cost, err := strconv.ParseFloat(m.Pricing.Completion, 64)
	if err != nil {
		return 0
	}
	return cost * 1000 // Convert from per-token to per-1K-tokens
}

// CalculateCost calculates the cost for given token counts
func (m *Model) CalculateCost(inputTokens, outputTokens int) float64 {
	inputCost := float64(inputTokens) / 1000.0 * m.GetInputCost()
	outputCost := float64(outputTokens) / 1000.0 * m.GetOutputCost()
	return inputCost + outputCost
}

// GetMaxOutputTokens returns the maximum output tokens
func (m *Model) GetMaxOutputTokens() int64 {
	if m.TopProvider != nil && m.TopProvider.MaxCompletionTokens > 0 {
		return m.TopProvider.MaxCompletionTokens
	}
	// Default to 25% of context length if not specified
	return m.ContextLength / 4
}

// HasVisionSupport returns true if the model supports vision/images
func (m *Model) HasVisionSupport() bool {
	if m.Architecture != nil {
		for _, modality := range m.Architecture.InputModalities {
			if modality == "image" {
				return true
			}
		}
	}
	return false
}

// Clone creates a deep copy of the model
func (m *Model) Clone() *Model {
	clone := *m
	
	// Deep copy Architecture
	if m.Architecture != nil {
		arch := *m.Architecture
		if m.Architecture.InputModalities != nil {
			arch.InputModalities = make([]string, len(m.Architecture.InputModalities))
			copy(arch.InputModalities, m.Architecture.InputModalities)
		}
		if m.Architecture.OutputModalities != nil {
			arch.OutputModalities = make([]string, len(m.Architecture.OutputModalities))
			copy(arch.OutputModalities, m.Architecture.OutputModalities)
		}
		clone.Architecture = &arch
	}
	
	// Deep copy Pricing
	if m.Pricing != nil {
		pricing := *m.Pricing
		clone.Pricing = &pricing
	}
	
	// Deep copy TopProvider
	if m.TopProvider != nil {
		provider := *m.TopProvider
		clone.TopProvider = &provider
	}
	
	// Deep copy slices
	if m.SupportedParameters != nil {
		clone.SupportedParameters = make([]string, len(m.SupportedParameters))
		copy(clone.SupportedParameters, m.SupportedParameters)
	}
	
	if m.Tags != nil {
		clone.Tags = make([]string, len(m.Tags))
		copy(clone.Tags, m.Tags)
	}
	
	// Deep copy map
	if m.PerRequestLimits != nil {
		clone.PerRequestLimits = make(map[string]interface{}, len(m.PerRequestLimits))
		for k, v := range m.PerRequestLimits {
			clone.PerRequestLimits[k] = v
		}
	}
	
	return &clone
}

// Validate checks if the model configuration is valid
func (m *Model) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("ID is required")
	}

	if m.Architecture == nil {
		return fmt.Errorf("Architecture is required")
	}

	if m.ContextLength <= 0 {
		return fmt.Errorf("ContextLength must be positive")
	}

	// Validate pricing if present
	if m.Pricing != nil {
		if m.Pricing.Prompt != "" {
			if _, err := strconv.ParseFloat(m.Pricing.Prompt, 64); err != nil {
				return fmt.Errorf("invalid prompt pricing: %w", err)
			}
		}
		if m.Pricing.Completion != "" {
			if _, err := strconv.ParseFloat(m.Pricing.Completion, 64); err != nil {
				return fmt.Errorf("invalid completion pricing: %w", err)
			}
		}
	}

	return nil
}

// ToOpenAI converts the model to OpenAI's minimal format
func (m *Model) ToOpenAI() OpenAIModel {
	// Derive owned_by from provider in ID or use default
	ownedBy := "system"
	if m.Provider != "" {
		ownedBy = m.Provider
	} else if idx := strings.Index(m.ID, "/"); idx > 0 {
		// Extract provider from ID if it's in provider/model format
		ownedBy = m.ID[:idx]
	}
	
	return OpenAIModel{
		ID:      m.ID,
		Object:  "model",
		Created: m.Created,
		OwnedBy: ownedBy,
	}
}

// ToOpenRouter returns the model in OpenRouter format (which is the native format)
// This method ensures all fields are properly set for API responses
func (m *Model) ToOpenRouter() *Model {
	// Ensure TopProvider is populated if we have relevant data
	if m.TopProvider == nil && m.ContextLength > 0 {
		m.TopProvider = &TopProvider{
			ContextLength: m.ContextLength,
		}
	}
	
	return m
}


