package routing

import (
	"strings"
)

// ModelSelector selects models for auto-routing
type ModelSelector interface {
	// SelectModels returns a list of models to try for auto-routing
	SelectModels(req *Request) []string
}

// defaultModelSelector implements basic model selection logic
type defaultModelSelector struct {
	// Model capabilities and preferences
	modelCapabilities map[string]ModelCapability
}

// ModelCapability describes what a model can do
type ModelCapability struct {
	Provider          string
	ContextLength     int
	MaxOutputTokens   int
	SupportsVision    bool
	SupportsFunctions bool
	SupportsStreaming bool
	CostPerMillion    float64 // Cost per million tokens
	LatencyClass      string   // "fast", "medium", "slow"
	Quality           string   // "economy", "standard", "premium"
}

// NewDefaultModelSelector creates a new model selector with default configurations
func NewDefaultModelSelector() ModelSelector {
	return &defaultModelSelector{
		modelCapabilities: defaultModelCapabilities(),
	}
}

// SelectModels returns models suitable for the request
func (s *defaultModelSelector) SelectModels(req *Request) []string {
	// Analyze request characteristics
	hasVision := s.requestHasVision(req)
	hasFunctions := s.requestHasFunctions(req)
	estimatedTokens := s.estimateTokens(req)
	
	// Build a list of suitable models
	var models []string
	
	// Start with fast, economical models
	if !hasVision && !hasFunctions && estimatedTokens < 4000 {
		// Simple requests can use fast models
		models = append(models, 
			"groq/llama-3.1-8b-instant",      // Ultra-fast
			"openai/gpt-3.5-turbo",           // Fast and cheap
			"anthropic/claude-3-haiku-20240307", // Fast Claude
		)
	}
	
	// Add standard models
	if hasVision {
		// Vision-capable models
		models = append(models,
			"openai/gpt-4-vision-preview",
			"anthropic/claude-3-sonnet-20240229",
			"google-aistudio/gemini-1.5-pro",
		)
	} else if hasFunctions {
		// Function-calling capable models
		models = append(models,
			"openai/gpt-4-turbo-preview",
			"anthropic/claude-3-sonnet-20240229",
			"mistral/mistral-large-latest",
		)
	} else {
		// Standard text models
		models = append(models,
			"openai/gpt-4-turbo-preview",
			"anthropic/claude-3-sonnet-20240229",
			"google-aistudio/gemini-1.5-pro",
		)
	}
	
	// Add premium models as final fallback
	models = append(models,
		"openai/gpt-4",
		"anthropic/claude-3-opus-20240229",
	)
	
	// Filter by metadata preferences if provided
	if req.Metadata != nil {
		models = s.filterByPreferences(models, req.Metadata)
	}
	
	// Remove duplicates while preserving order
	return s.deduplicateModels(models)
}

// requestHasVision checks if the request contains images
func (s *defaultModelSelector) requestHasVision(req *Request) bool {
	for _, msg := range req.Messages {
		// Check if content is multimodal
		if parts, ok := msg.Content.([]interface{}); ok {
			for _, part := range parts {
				if p, ok := part.(map[string]interface{}); ok {
					if p["type"] == "image_url" {
						return true
					}
				}
			}
		}
	}
	return false
}

// requestHasFunctions checks if the request uses function calling
func (s *defaultModelSelector) requestHasFunctions(req *Request) bool {
	return len(req.Tools) > 0
}

// estimateTokens provides a rough estimate of tokens in the request
func (s *defaultModelSelector) estimateTokens(req *Request) int {
	// Very rough estimation: ~4 characters per token
	totalChars := 0
	
	for _, msg := range req.Messages {
		switch content := msg.Content.(type) {
		case string:
			totalChars += len(content)
		case []interface{}:
			// Multimodal content
			for _, part := range content {
				if p, ok := part.(map[string]interface{}); ok {
					if text, ok := p["text"].(string); ok {
						totalChars += len(text)
					}
					// Images count as ~1000 tokens each (rough estimate)
					if p["type"] == "image_url" {
						totalChars += 4000
					}
				}
			}
		}
	}
	
	// Add some buffer for response
	if req.MaxTokens != nil {
		totalChars += *req.MaxTokens * 4
	} else {
		totalChars += 2000 // Default buffer
	}
	
	return totalChars / 4
}

// filterByPreferences filters models based on user preferences
func (s *defaultModelSelector) filterByPreferences(models []string, metadata *RequestMetadata) []string {
	if metadata.UserPreferences == nil {
		return models
	}
	
	// Check for quality preference
	if quality, ok := metadata.UserPreferences["quality"].(string); ok {
		filtered := []string{}
		for _, model := range models {
			if s.matchesQuality(model, quality) {
				filtered = append(filtered, model)
			}
		}
		if len(filtered) > 0 {
			models = filtered
		}
	}
	
	// Check for speed preference
	if speed, ok := metadata.UserPreferences["speed"].(string); ok {
		filtered := []string{}
		for _, model := range models {
			if s.matchesSpeed(model, speed) {
				filtered = append(filtered, model)
			}
		}
		if len(filtered) > 0 {
			models = filtered
		}
	}
	
	return models
}

// matchesQuality checks if a model matches the quality preference
func (s *defaultModelSelector) matchesQuality(model, quality string) bool {
	caps, ok := s.modelCapabilities[model]
	if !ok {
		return true // Unknown models pass through
	}
	
	switch quality {
	case "economy":
		return caps.Quality == "economy"
	case "premium":
		return caps.Quality == "premium"
	default:
		return true
	}
}

// matchesSpeed checks if a model matches the speed preference  
func (s *defaultModelSelector) matchesSpeed(model, speed string) bool {
	caps, ok := s.modelCapabilities[model]
	if !ok {
		return true // Unknown models pass through
	}
	
	switch speed {
	case "fast":
		return caps.LatencyClass == "fast"
	case "slow":
		return caps.LatencyClass == "slow"
	default:
		return true
	}
}

// deduplicateModels removes duplicates while preserving order
func (s *defaultModelSelector) deduplicateModels(models []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	
	for _, model := range models {
		if !seen[model] {
			seen[model] = true
			result = append(result, model)
		}
	}
	
	return result
}

// defaultModelCapabilities returns default model capabilities
func defaultModelCapabilities() map[string]ModelCapability {
	return map[string]ModelCapability{
		// OpenAI models
		"openai/gpt-4": {
			Provider:          "openai",
			ContextLength:     8192,
			MaxOutputTokens:   4096,
			SupportsVision:    false,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    30.0,
			LatencyClass:      "slow",
			Quality:           "premium",
		},
		"openai/gpt-4-turbo-preview": {
			Provider:          "openai",
			ContextLength:     128000,
			MaxOutputTokens:   4096,
			SupportsVision:    false,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    10.0,
			LatencyClass:      "medium",
			Quality:           "premium",
		},
		"openai/gpt-4-vision-preview": {
			Provider:          "openai",
			ContextLength:     128000,
			MaxOutputTokens:   4096,
			SupportsVision:    true,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    10.0,
			LatencyClass:      "medium",
			Quality:           "premium",
		},
		"openai/gpt-3.5-turbo": {
			Provider:          "openai",
			ContextLength:     16385,
			MaxOutputTokens:   4096,
			SupportsVision:    false,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    0.5,
			LatencyClass:      "fast",
			Quality:           "economy",
		},
		
		// Anthropic models
		"anthropic/claude-3-opus-20240229": {
			Provider:          "anthropic",
			ContextLength:     200000,
			MaxOutputTokens:   4096,
			SupportsVision:    true,
			SupportsFunctions: false,
			SupportsStreaming: true,
			CostPerMillion:    15.0,
			LatencyClass:      "slow",
			Quality:           "premium",
		},
		"anthropic/claude-3-sonnet-20240229": {
			Provider:          "anthropic",
			ContextLength:     200000,
			MaxOutputTokens:   4096,
			SupportsVision:    true,
			SupportsFunctions: false,
			SupportsStreaming: true,
			CostPerMillion:    3.0,
			LatencyClass:      "medium",
			Quality:           "standard",
		},
		"anthropic/claude-3-haiku-20240307": {
			Provider:          "anthropic",
			ContextLength:     200000,
			MaxOutputTokens:   4096,
			SupportsVision:    true,
			SupportsFunctions: false,
			SupportsStreaming: true,
			CostPerMillion:    0.25,
			LatencyClass:      "fast",
			Quality:           "economy",
		},
		
		// Google AI Studio models
		"google-aistudio/gemini-1.5-pro": {
			Provider:          "google-aistudio",
			ContextLength:     1000000,
			MaxOutputTokens:   8192,
			SupportsVision:    true,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    7.0,
			LatencyClass:      "medium",
			Quality:           "premium",
		},
		"google-aistudio/gemini-1.5-flash": {
			Provider:          "google-aistudio",
			ContextLength:     1000000,
			MaxOutputTokens:   8192,
			SupportsVision:    true,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    0.7,
			LatencyClass:      "fast",
			Quality:           "economy",
		},
		
		// Google Vertex AI models
		"google-vertexai/gemini-1.5-pro": {
			Provider:          "google-vertexai",
			ContextLength:     1000000,
			MaxOutputTokens:   8192,
			SupportsVision:    true,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    2.5,
			LatencyClass:      "medium",
			Quality:           "premium",
		},
		"google-vertexai/gemini-1.5-flash": {
			Provider:          "google-vertexai",
			ContextLength:     1000000,
			MaxOutputTokens:   8192,
			SupportsVision:    true,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    0.25,
			LatencyClass:      "fast",
			Quality:           "economy",
		},
		"google-vertexai/claude-3-opus@20240229": {
			Provider:          "google-vertexai",
			ContextLength:     200000,
			MaxOutputTokens:   4096,
			SupportsVision:    true,
			SupportsFunctions: false,
			SupportsStreaming: true,
			CostPerMillion:    15.0,
			LatencyClass:      "slow",
			Quality:           "premium",
		},
		
		// Groq models
		"groq/llama-3.1-8b-instant": {
			Provider:          "groq",
			ContextLength:     128000,
			MaxOutputTokens:   8192,
			SupportsVision:    false,
			SupportsFunctions: false,
			SupportsStreaming: true,
			CostPerMillion:    0.05,
			LatencyClass:      "fast",
			Quality:           "economy",
		},
		
		// Mistral models
		"mistral/mistral-large-latest": {
			Provider:          "mistral",
			ContextLength:     32000,
			MaxOutputTokens:   4096,
			SupportsVision:    false,
			SupportsFunctions: true,
			SupportsStreaming: true,
			CostPerMillion:    8.0,
			LatencyClass:      "medium",
			Quality:           "standard",
		},
	}
}

// GetModelCapability returns the capability for a specific model
func GetModelCapability(modelID string) (ModelCapability, bool) {
	caps := defaultModelCapabilities()
	capability, ok := caps[modelID]
	return capability, ok
}

// IsModelAvailable checks if a model is available based on required features
func IsModelAvailable(modelID string, requiredFeatures []string) bool {
	capability, ok := GetModelCapability(modelID)
	if !ok {
		// Unknown model, assume it's available
		return true
	}
	
	for _, feature := range requiredFeatures {
		switch strings.ToLower(feature) {
		case "vision":
			if !capability.SupportsVision {
				return false
			}
		case "functions", "function_calling", "tools":
			if !capability.SupportsFunctions {
				return false
			}
		case "streaming":
			if !capability.SupportsStreaming {
				return false
			}
		}
	}
	
	return true
}