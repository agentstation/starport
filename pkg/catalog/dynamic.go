package catalog

import (
	"sync"
	"time"
)

var (
	// dynamicModels stores models discovered at runtime (e.g., from Ollama)
	dynamicModels = make(map[string]*Model)
	dynamicMutex  sync.RWMutex
)

// DynamicModelInfo represents the minimal info needed to register a dynamic model
type DynamicModelInfo struct {
	ID      string
	Created int64
	OwnedBy string
}

// RegisterDynamicModel registers a model discovered at runtime
func RegisterDynamicModel(_ string, modelInfo DynamicModelInfo) error {
	dynamicMutex.Lock()
	defer dynamicMutex.Unlock()

	// Create catalog model
	catalogModel := &Model{
		ID:            modelInfo.ID,
		Name:          modelInfo.ID, // Use ID as name for dynamic models
		Created:       modelInfo.Created,
		ContextLength: 4096,     // Default context length for Ollama models
		// Add minimal pricing info (Ollama is free/local)
		Pricing: &Pricing{
			Prompt:     "0",
			Completion: "0",
		},
		// Mark as text output model
		HasTextOutput: true,
		UpdatedAt:     time.Now().Format(time.RFC3339),
	}

	// Store in dynamic models map
	dynamicModels[modelInfo.ID] = catalogModel

	return nil
}

// GetDynamicModels returns all dynamically registered models
func GetDynamicModels() map[string]*Model {
	dynamicMutex.RLock()
	defer dynamicMutex.RUnlock()

	// Return a copy to avoid concurrent modification
	result := make(map[string]*Model, len(dynamicModels))
	for k, v := range dynamicModels {
		result[k] = v
	}
	return result
}

// GetModelsByProviderWithDynamic extends GetModelsByProviderWithMapping to include dynamic models
func GetModelsByProviderWithDynamic(provider string) []*Model {
	// First get static models from catalog
	cat, err := GetCatalog()
	if err != nil {
		// If catalog fails, just return dynamic models
		dynamicMutex.RLock()
		defer dynamicMutex.RUnlock()
		
		var models []*Model
		for id, model := range dynamicModels {
			if len(id) > len(provider)+1 && id[:len(provider)+1] == provider+"/" {
				models = append(models, model)
			}
		}
		return models
	}
	
	staticModels := cat.GetModelsByProviderWithMapping(provider)
	
	// Then add dynamic models for this provider
	dynamicMutex.RLock()
	defer dynamicMutex.RUnlock()
	
	for id, model := range dynamicModels {
		// Check if this model belongs to the requested provider
		if len(id) > len(provider)+1 && id[:len(provider)+1] == provider+"/" {
			staticModels = append(staticModels, model)
		}
	}
	
	return staticModels
}

// ClearDynamicModels clears all dynamic models (useful for testing)
func ClearDynamicModels() {
	dynamicMutex.Lock()
	defer dynamicMutex.Unlock()
	dynamicModels = make(map[string]*Model)
}