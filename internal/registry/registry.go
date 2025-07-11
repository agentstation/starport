// Package registry manages LLM provider connectors
package registry

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/providers/connectors"
)

// Registry manages provider connectors
type Registry struct {
	connectors map[string]connectors.Connector
	mu         sync.RWMutex
}

// New creates a new connector registry
func New() *Registry {
	return &Registry{
		connectors: make(map[string]connectors.Connector),
	}
}

// Register adds a connector to the registry
func (r *Registry) Register(provider string, connector connectors.Connector) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.connectors[provider]; exists {
		return fmt.Errorf("connector already registered for provider: %s", provider)
	}

	r.connectors[provider] = connector
	log.Info().
		Str("provider", provider).
		Msg("registered connector")

	return nil
}

// Get retrieves a connector by provider name
func (r *Registry) Get(provider string) (connectors.Connector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	connector, exists := r.connectors[provider]
	if !exists {
		return nil, fmt.Errorf("no connector registered for provider: %s", provider)
	}

	return connector, nil
}

// GetAll returns all registered connectors
func (r *Registry) GetAll() map[string]connectors.Connector {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent concurrent modification
	result := make(map[string]connectors.Connector, len(r.connectors))
	for k, v := range r.connectors {
		result[k] = v
	}

	return result
}

// ListProviders returns a list of registered provider names
func (r *Registry) ListProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]string, 0, len(r.connectors))
	for provider := range r.connectors {
		providers = append(providers, provider)
	}

	return providers
}

// HasProvider checks if a provider is registered
func (r *Registry) HasProvider(provider string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.connectors[provider]
	return exists
}

// Close closes all connectors
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error

	for provider, connector := range r.connectors {
		if err := connector.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close %s connector: %w", provider, err))
		}
	}

	// Clear the map
	r.connectors = make(map[string]connectors.Connector)

	if len(errs) > 0 {
		return fmt.Errorf("errors closing connectors: %v", errs)
	}

	return nil
}

// HealthCheck performs health checks on all registered connectors
func (r *Registry) HealthCheck(ctx context.Context) map[string]error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make(map[string]error)

	for provider, connector := range r.connectors {
		err := connector.Health(ctx)
		results[provider] = err
	}

	return results
}

// GetModels returns all available models from all providers
func (r *Registry) GetModels(ctx context.Context) ([]connectors.Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var allModels []connectors.Model

	for _, connector := range r.connectors {
		modelsResp, err := connector.Models(ctx)
		if err != nil {
			// Log error but continue with other providers
			log.Warn().
				Err(err).
				Msg("failed to get models from provider")
			continue
		}

		if modelsResp != nil && modelsResp.Data != nil {
			allModels = append(allModels, modelsResp.Data...)
		}
	}

	return allModels, nil
}

// GetProviderMetadata returns metadata for all registered providers
func (r *Registry) GetProviderMetadata() []connectors.ProviderMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get all provider metadata
	allMetadata := connectors.GetProviderMetadata()

	// Filter to only include registered providers
	var registeredMetadata []connectors.ProviderMetadata
	for _, meta := range allMetadata {
		if _, exists := r.connectors[meta.Slug]; exists {
			registeredMetadata = append(registeredMetadata, meta)
		}
	}

	return registeredMetadata
}
