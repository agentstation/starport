package providers

import (
	"fmt"
	"sync"
)

// Registry is a thread-safe storage for providers.
// It implements a simple map with concurrent access protection.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]*Provider
}

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]*Provider),
	}
}

// Add adds a provider to the registry.
// If a provider with the same ID already exists, it will be replaced.
func (r *Registry) Add(provider *Provider) error {
	if provider == nil {
		return fmt.Errorf("provider cannot be nil")
	}

	if provider.ID == "" {
		return &ValidationError{
			Field:   "ID",
			Message: "provider ID cannot be empty",
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers[provider.ID] = provider
	return nil
}

// Get retrieves a provider by ID.
// Returns ErrProviderNotFound if the provider doesn't exist.
// Returns ErrProviderDisabled if the provider is disabled.
// Returns ErrNoConnector if the provider has no connector.
func (r *Registry) Get(id string) (*Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}

	if !provider.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrProviderDisabled, id)
	}

	if provider.Connector == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoConnector, id)
	}

	return provider, nil
}

// GetUnsafe retrieves a provider without checking if it's enabled or has a connector.
// This is useful for administrative operations.
func (r *Registry) GetUnsafe(id string) (*Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}

	return provider, nil
}

// List returns all providers (both enabled and disabled).
// The returned slice is a copy and safe to modify.
func (r *Registry) List() []*Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providers := make([]*Provider, 0, len(r.providers))
	for _, p := range r.providers {
		providers = append(providers, p)
	}

	return providers
}

// ListEnabled returns only enabled providers with connectors.
// The returned slice is a copy and safe to modify.
func (r *Registry) ListEnabled() []*Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var providers []*Provider
	for _, p := range r.providers {
		if p.Enabled && p.Connector != nil {
			providers = append(providers, p)
		}
	}

	return providers
}

// ListByType returns all enabled providers of a specific type.
// For example, ListByType("openai") returns all OpenAI-based providers.
func (r *Registry) ListByType(providerType string) []*Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var providers []*Provider
	for _, p := range r.providers {
		if p.Type == providerType && p.Enabled && p.Connector != nil {
			providers = append(providers, p)
		}
	}

	return providers
}

// Has checks if a provider exists in the registry
func (r *Registry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.providers[id]
	return ok
}

// Remove removes a provider from the registry
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.providers[id]; !ok {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}

	delete(r.providers, id)
	return nil
}

// Update updates an existing provider.
// The provider must already exist in the registry.
func (r *Registry) Update(provider *Provider) error {
	if provider == nil {
		return fmt.Errorf("provider cannot be nil")
	}

	if provider.ID == "" {
		return &ValidationError{
			Field:   "ID",
			Message: "provider ID cannot be empty",
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.providers[provider.ID]; !ok {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, provider.ID)
	}

	r.providers[provider.ID] = provider
	return nil
}

// Count returns the total number of providers
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.providers)
}

// CountEnabled returns the number of enabled providers
func (r *Registry) CountEnabled() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, p := range r.providers {
		if p.Enabled && p.Connector != nil {
			count++
		}
	}

	return count
}

// Clear removes all providers from the registry
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers = make(map[string]*Provider)
}

// IDs returns a list of all provider IDs
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}

	return ids
}

// FindByModel returns all providers that have a specific model
func (r *Registry) FindByModel(modelID string) []*Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var providers []*Provider
	for _, p := range r.providers {
		if p.Enabled && p.Connector != nil && p.HasModel(modelID) {
			providers = append(providers, p)
		}
	}

	return providers
}