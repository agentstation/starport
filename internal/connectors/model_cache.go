package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// modelCache provides caching for model responses
type modelCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	models    *ModelsResponse
	expiresAt time.Time
}

var (
	// Global model cache instance
	globalModelCache = &modelCache{
		entries: make(map[string]*cacheEntry),
	}
	
	// Default cache TTL of 1 hour
	modelCacheTTL = 1 * time.Hour
)

// getCachedModels retrieves cached models for a provider
func getCachedModels(provider string) (*ModelsResponse, bool) {
	globalModelCache.mu.RLock()
	defer globalModelCache.mu.RUnlock()
	
	entry, exists := globalModelCache.entries[provider]
	if !exists || entry.expiresAt.Before(time.Now()) {
		return nil, false
	}
	
	return entry.models, true
}

// setCachedModels stores models in cache
func setCachedModels(provider string, models *ModelsResponse) {
	globalModelCache.mu.Lock()
	defer globalModelCache.mu.Unlock()
	
	globalModelCache.entries[provider] = &cacheEntry{
		models:    models,
		expiresAt: time.Now().Add(modelCacheTTL),
	}
}

// clearModelCache clears the cache for a provider
// Used for testing purposes
//
//nolint:unused // Used in tests
func clearModelCache(provider string) {
	globalModelCache.mu.Lock()
	defer globalModelCache.mu.Unlock()
	
	delete(globalModelCache.entries, provider)
}

// fetchModelsWithCache fetches models with caching support
func fetchModelsWithCache(ctx context.Context, provider string, fetcher func(context.Context) (*ModelsResponse, error)) (*ModelsResponse, error) {
	// Check cache first
	if cached, ok := getCachedModels(provider); ok {
		return cached, nil
	}
	
	// Fetch from API
	models, err := fetcher(ctx)
	if err != nil {
		return nil, err
	}
	
	// Cache the result
	setCachedModels(provider, models)
	
	return models, nil
}

// parseModelsResponse is a helper to parse JSON response into ModelsResponse
func parseModelsResponse(data []byte, provider string) (*ModelsResponse, error) {
	var response struct {
		Data []struct {
			ID            string `json:"id"`
			Created       int64  `json:"created,omitempty"`
			CreatedAtStr  string `json:"created_at,omitempty"` // Some APIs use string format
			DisplayName   string `json:"display_name,omitempty"`
			Type          string `json:"type,omitempty"`
			Object        string `json:"object,omitempty"`
		} `json:"data"`
		Models []struct {
			ID          string `json:"id"`
			Created     int64  `json:"created"`
			DisplayName string `json:"display_name,omitempty"`
			Object      string `json:"object,omitempty"`
		} `json:"models"`
	}
	
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("failed to parse models response: %w", err)
	}
	
	models := make([]Model, 0)
	
	// Handle different response formats
	if len(response.Data) > 0 {
		// Anthropic format
		for _, m := range response.Data {
			created := m.Created
			if created == 0 && m.CreatedAtStr != "" {
				// Try to parse string date
				if t, err := time.Parse(time.RFC3339, m.CreatedAtStr); err == nil {
					created = t.Unix()
				}
			}
			models = append(models, Model{
				ID:      fmt.Sprintf("%s/%s", provider, m.ID),
				Object:  "model",
				Created: created,
				OwnedBy: provider,
			})
		}
	} else if len(response.Models) > 0 {
		// OpenAI/Groq format
		for _, m := range response.Models {
			models = append(models, Model{
				ID:      fmt.Sprintf("%s/%s", provider, m.ID),
				Object:  m.Object,
				Created: m.Created,
				OwnedBy: provider,
			})
		}
	}
	
	return &ModelsResponse{
		Object: "list",
		Data:   models,
	}, nil
}