// Package registry manages LLM provider connectors
package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/rs/zerolog/log"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
)

var (
	// ErrCatalogRequired reports an absent Starmap control plane.
	ErrCatalogRequired = errors.New("registry catalog is required")
	// ErrProvidersRequired reports an empty provider registration set.
	ErrProvidersRequired = errors.New("at least one provider registration is required")
	// ErrProviderRequired reports a registration without a provider ID.
	ErrProviderRequired = errors.New("provider registration name is required")
	// ErrConnectorRequired reports a registration without a connector.
	ErrConnectorRequired = errors.New("provider connector is required")
	// ErrRegistryStarted reports a second start request.
	ErrRegistryStarted = errors.New("registry already started")
	// ErrRegistryClosed reports an operation after registry shutdown.
	ErrRegistryClosed = errors.New("registry is closed")
)

// Registration binds one configured connector to a provider ID.
type Registration struct {
	Provider         string
	Connector        connectors.Connector
	Operations       []starmapcatalogs.ProviderOperation
	EndpointTypes    []starmapcatalogs.EndpointType
	BaseURL          string
	EndpointBindings map[string]string
	RequiresAuth     bool
}

// ProviderMetadata contains catalog facts needed by gateway provider discovery.
type ProviderMetadata struct {
	ID           string
	Name         string
	URL          string
	Models       []string
	Capabilities []string
	RequiresAuth bool
}

// Registry manages provider connectors and their lifecycle
type Registry struct {
	connectors   map[string]connectors.Connector
	providerAuth map[string]bool
	mu           sync.RWMutex
	catalog      *runtimecatalog.ControlPlane
	lifecycleMu  sync.Mutex
	started      bool
	closed       bool
}

// Open creates a registry from explicit production registrations.
func Open(catalogPlane *runtimecatalog.ControlPlane, registrations []Registration) (*Registry, error) {
	if catalogPlane == nil {
		return nil, ErrCatalogRequired
	}
	if len(registrations) == 0 {
		return nil, ErrProvidersRequired
	}
	registry := NewEmptyWithCatalog(catalogPlane)
	for index, registration := range registrations {
		if registration.Provider == "" {
			return nil, registry.openFailure(ErrProviderRequired, registrations[index:])
		}
		if registration.Connector == nil {
			return nil, registry.openFailure(
				fmt.Errorf("%s: %w", registration.Provider, ErrConnectorRequired),
				registrations[index:],
			)
		}
		if err := registry.register(registration); err != nil {
			return nil, registry.openFailure(
				fmt.Errorf("register %s: %w", registration.Provider, err),
				registrations[index:],
			)
		}
	}
	return registry, nil
}

// NewEmpty creates an empty registry without initialization
// Useful for testing
func NewEmpty() *Registry {
	return NewEmptyWithCatalog(nil)
}

// NewEmptyWithCatalog creates an empty registry with a catalog control plane.
func NewEmptyWithCatalog(catalogPlane *runtimecatalog.ControlPlane) *Registry {
	return &Registry{
		connectors:   make(map[string]connectors.Connector),
		providerAuth: make(map[string]bool),
		catalog:      catalogPlane,
	}
}

// Start seals the registry against later registrations.
func (r *Registry) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("registry start context is required")
	}
	r.lifecycleMu.Lock()
	if r.closed {
		r.lifecycleMu.Unlock()
		return ErrRegistryClosed
	}
	if r.started {
		r.lifecycleMu.Unlock()
		return nil
	}
	r.started = true
	r.lifecycleMu.Unlock()

	return nil
}

// Register adds a connector to the registry
func (r *Registry) Register(provider string, connector connectors.Connector) error {
	registration := Registration{Provider: provider, Connector: connector}
	adapters, err := connectors.ProductionAdapterRegistry()
	if err != nil {
		return fmt.Errorf("load production adapter registry: %w", err)
	}
	if descriptor, found := adapters.Descriptor(starmapcatalogs.ProviderID(provider)); found {
		registration.Operations = descriptor.Operations
		registration.EndpointTypes = descriptor.EndpointTypes
		registration.RequiresAuth = len(descriptor.Credential.Fields) > 0
	}
	return r.register(registration)
}

func (r *Registry) register(registration Registration) error {
	provider := registration.Provider
	connector := registration.Connector
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	if r.started {
		return ErrRegistryStarted
	}

	r.mu.Lock()
	if _, exists := r.connectors[provider]; exists {
		r.mu.Unlock()
		return fmt.Errorf("connector already registered for provider: %s", provider)
	}

	r.connectors[provider] = connector
	r.providerAuth[provider] = registration.RequiresAuth
	r.mu.Unlock()
	if r.catalog != nil {
		if err := r.catalog.SetAdapter(runtimecatalog.AdapterAvailability{
			ProviderID:       starmapcatalogs.ProviderID(provider),
			Registered:       true,
			Configured:       true,
			Operations:       append([]starmapcatalogs.ProviderOperation(nil), registration.Operations...),
			EndpointTypes:    append([]starmapcatalogs.EndpointType(nil), registration.EndpointTypes...),
			BaseURL:          registration.BaseURL,
			EndpointBindings: cloneStringMap(registration.EndpointBindings),
		}); err != nil {
			r.mu.Lock()
			delete(r.connectors, provider)
			delete(r.providerAuth, provider)
			r.mu.Unlock()
			return fmt.Errorf("publish %s adapter availability: %w", provider, err)
		}
	}

	log.Info().
		Str("provider", provider).
		Msg("registered connector")

	return nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (r *Registry) openFailure(cause error, unowned []Registration) error {
	errs := []error{cause}
	if err := r.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close registered connectors: %w", err))
	}
	for index := len(unowned) - 1; index >= 0; index-- {
		registration := unowned[index]
		if registration.Connector == nil {
			continue
		}
		if err := registration.Connector.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close unregistered %s connector: %w", registration.Provider, err))
		}
	}
	return errors.Join(errs...)
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

// IsProviderConfigured reports whether app composition registered the provider.
func (r *Registry) IsProviderConfigured(provider string) bool {
	return r.HasProvider(provider)
}

// Catalog returns the registry's shared catalog control plane.
func (r *Registry) Catalog() *runtimecatalog.ControlPlane {
	if r == nil {
		return nil
	}
	return r.catalog
}

// Close closes all connectors
func (r *Registry) Close() error {
	r.lifecycleMu.Lock()
	if r.closed {
		r.lifecycleMu.Unlock()
		return nil
	}
	r.closed = true
	r.lifecycleMu.Unlock()

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
	r.providerAuth = make(map[string]bool)
	if r.catalog != nil {
		if err := r.catalog.ReplaceAdapters(nil); err != nil {
			errs = append(errs, fmt.Errorf("clear adapter availability: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing connectors: %v", errs)
	}

	return nil
}

// GetProviderMetadata returns Starmap metadata for registered providers.
func (r *Registry) GetProviderMetadata() []ProviderMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := r.catalogSnapshot()
	if snapshot == nil {
		return nil
	}
	seen := make(map[starmapcatalogs.ProviderID]struct{})
	metadata := make([]ProviderMetadata, 0)
	for _, route := range snapshot.Routes() {
		if _, exists := seen[route.ProviderID]; exists {
			continue
		}
		provider, err := snapshot.Catalog().Provider(route.ProviderID)
		if err != nil {
			continue
		}
		item := ProviderMetadata{
			ID:   string(provider.ID),
			Name: provider.Name,
		}
		if provider.StatusPageURL != nil {
			item.URL = *provider.StatusPageURL
		}
		capabilities := make(map[string]struct{})
		for _, providerRoute := range snapshot.RoutesForProvider(route.ProviderID) {
			item.Models = append(item.Models, providerRoute.ID())
			for _, operation := range providerRoute.Operations {
				capabilities[string(operation)] = struct{}{}
			}
		}
		for capability := range capabilities {
			item.Capabilities = append(item.Capabilities, capability)
		}
		sort.Strings(item.Capabilities)
		item.RequiresAuth = r.providerAuth[string(route.ProviderID)]
		seen[route.ProviderID] = struct{}{}
		metadata = append(metadata, item)
	}
	return metadata
}

func (r *Registry) catalogSnapshot() *runtimecatalog.RoutableSnapshot {
	if r == nil || r.catalog == nil {
		return nil
	}
	return r.catalog.Current()
}
