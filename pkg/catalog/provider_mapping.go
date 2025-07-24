package catalog

import "strings"

// providerMapping handles the mapping between model prefixes and provider names
var providerMapping = map[string]string{
	"google": "google-ai-studio", // Default Google models to AI Studio
}

// GetProviderForModel determines the provider for a given model ID
func GetProviderForModel(modelID string) string {
	// Extract the prefix (provider part) from the model ID
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) < 2 {
		return ""
	}
	
	prefix := parts[0]
	modelName := parts[1]
	
	// Special handling for Google models
	if prefix == "google" {
		// Vertex AI specific models
		if strings.Contains(modelName, "@") || // Vertex AI uses @ for versioning
			strings.HasPrefix(modelName, "text-bison") ||
			strings.HasPrefix(modelName, "code-bison") ||
			strings.HasPrefix(modelName, "claude-") { // Claude via Model Garden
			return "google-vertex"
		}
		// Default to AI Studio for Gemini models
		return "google-ai-studio"
	}
	
	// Direct mapping for other providers
	if mapped, ok := providerMapping[prefix]; ok {
		return mapped
	}
	
	// Return the prefix as-is for other providers
	return prefix
}

// GetModelsByProviderWithMapping returns models for a provider, handling prefix mappings
func (c *Catalog) GetModelsByProviderWithMapping(provider string) []*Model {
	var result []*Model
	
	for modelID, model := range c.Models {
		modelProvider := GetProviderForModel(modelID)
		if modelProvider == provider {
			result = append(result, model)
		}
	}
	
	return result
}