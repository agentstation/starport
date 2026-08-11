// Package registry manages LLM provider connectors
package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
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
	// ErrCredentialSourceRequired reports a production registration without a
	// request-time material source.
	ErrCredentialSourceRequired = errors.New("provider credential material source is required")
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
	CredentialSource credentials.MaterialSource
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
	catalog *runtimecatalog.ControlPlane
	current atomic.Pointer[runtimeGeneration]

	lifecycleMu sync.Mutex
	started     bool
	closed      bool

	drainMu     sync.Mutex
	drainErrors []error
}

// Open creates a registry from explicit production registrations.
func Open(catalogPlane *runtimecatalog.ControlPlane, registrations []Registration) (*Registry, error) {
	if catalogPlane == nil {
		return nil, ErrCatalogRequired
	}
	registry := NewEmptyWithCatalog(catalogPlane)
	candidate, err := prepareCandidate(registrations, true)
	if err != nil {
		return nil, err
	}
	if err := catalogPlane.ReplaceAdapters(candidate.Availability()); err != nil {
		return nil, errors.Join(err, candidate.Close())
	}
	if err := registry.Publish(candidate, catalogPlane.Current()); err != nil {
		return nil, errors.Join(err, candidate.Close())
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
	return &Registry{catalog: catalogPlane}
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
	if provider == "" {
		return ErrProviderRequired
	}
	if connector == nil {
		return fmt.Errorf("%s: %w", provider, ErrConnectorRequired)
	}
	registration := Registration{Provider: provider, Connector: connector}
	if snapshot := r.catalogSnapshot(); snapshot != nil {
		operations := make(map[starmapcatalogs.ProviderOperation]struct{})
		endpointTypes := make(map[starmapcatalogs.EndpointType]struct{})
		providerID := starmapcatalogs.ProviderID(provider)
		if providerRecord, lookupErr := snapshot.Catalog().Provider(providerID); lookupErr == nil && providerRecord.Credentials != nil {
			profiles := make(map[starmapcatalogs.ProviderCredentialProfileID]starmapcatalogs.ProviderCredentialProfile)
			for _, profile := range providerRecord.Credentials.Profiles {
				profiles[profile.ID] = profile
			}
			for _, profileID := range providerRecord.Credentials.Inference.Alternatives {
				if profile, exists := profiles[profileID]; exists &&
					profile.Primitive != starmapcatalogs.ProviderAuthenticationNone {
					registration.RequiresAuth = true
					break
				}
			}
		}
		offerings, _ := snapshot.Catalog().ProviderOfferings(providerID)
		for _, offering := range offerings {
			for _, operation := range offering.Service.Operations {
				operations[operation] = struct{}{}
				if endpoint, found := offering.Endpoint(operation); found {
					endpointTypes[endpoint.Type] = struct{}{}
				}
			}
		}
		for operation := range operations {
			registration.Operations = append(registration.Operations, operation)
		}
		for endpointType := range endpointTypes {
			registration.EndpointTypes = append(registration.EndpointTypes, endpointType)
		}
		sort.Slice(registration.Operations, func(left, right int) bool {
			return registration.Operations[left] < registration.Operations[right]
		})
		sort.Slice(registration.EndpointTypes, func(left, right int) bool {
			return registration.EndpointTypes[left] < registration.EndpointTypes[right]
		})
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	if r.started {
		return ErrRegistryStarted
	}
	if current := r.current.Load(); current != nil && current.connector(provider) != nil {
		return fmt.Errorf("connector already registered for provider: %s", provider)
	}
	registrations := []Registration{registration}
	if current := r.current.Load(); current != nil {
		registrations = append(current.registrations(), registration)
	}
	candidate, err := prepareCandidate(registrations, false)
	if err != nil {
		return err
	}
	if r.catalog != nil {
		if err := r.catalog.ReplaceAdapters(candidate.Availability()); err != nil {
			closeErr := connector.Close()
			if closeErr != nil {
				closeErr = fmt.Errorf("close unowned %s connector: %w", provider, closeErr)
			}
			return errors.Join(err, closeErr)
		}
	}
	generation, err := candidate.consume()
	if err != nil {
		return err
	}
	generation.catalog = r.catalog
	if r.catalog != nil {
		generation.snapshot = r.catalog.Current()
		if generation.snapshot != nil {
			generation.catalogGenerationID = generation.snapshot.GenerationID()
		}
	}
	// Register is a construction-only API. The new generation reuses prior
	// connectors, so it replaces the unpublished construction view without
	// draining it.
	r.current.Store(generation)
	log.Info().
		Str("provider", registration.Provider).
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

// Get retrieves a connector by provider name
func (r *Registry) Get(provider string) (connectors.Connector, error) {
	generation := r.current.Load()
	if generation == nil {
		return nil, fmt.Errorf("no connector registered for provider: %s", provider)
	}
	connector := generation.connector(provider)
	if connector == nil {
		return nil, fmt.Errorf("no connector registered for provider: %s", provider)
	}

	return connector, nil
}

// ResolveMaterial resolves request-bound inference material for one exact
// registered provider.
func (r *Registry) ResolveMaterial(ctx context.Context, provider string) (credentials.Material, error) {
	generation := r.current.Load()
	if generation == nil {
		return credentials.Material{}, ErrProvidersRequired
	}
	return generation.resolveMaterial(ctx, provider)
}

// GetAll returns all registered connectors
func (r *Registry) GetAll() map[string]connectors.Connector {
	generation := r.current.Load()
	if generation == nil {
		return map[string]connectors.Connector{}
	}
	result := make(map[string]connectors.Connector, len(generation.providers))
	for providerID, entry := range generation.providers {
		result[providerID] = entry.registration.Connector
	}

	return result
}

// ListProviders returns a list of registered provider names
func (r *Registry) ListProviders() []string {
	generation := r.current.Load()
	if generation == nil {
		return nil
	}
	providers := make([]string, 0, len(generation.providers))
	for provider := range generation.providers {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

// HasProvider checks if a provider is registered
func (r *Registry) HasProvider(provider string) bool {
	generation := r.current.Load()
	return generation != nil && generation.connector(provider) != nil
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

	current := r.current.Swap(nil)
	if current != nil {
		current.drain()
	}
	var errs []error
	if r.catalog != nil {
		if err := r.catalog.ReplaceAdapters(nil); err != nil {
			errs = append(errs, fmt.Errorf("clear adapter availability: %w", err))
		}
	}

	r.drainMu.Lock()
	errs = append(errs, r.drainErrors...)
	r.drainMu.Unlock()
	return errors.Join(errs...)
}

// GetProviderMetadata returns Starmap metadata for registered providers.
func (r *Registry) GetProviderMetadata() []ProviderMetadata {
	lease, err := r.AcquireRuntime()
	if err != nil {
		return nil
	}
	defer lease.Release()
	return r.GetProviderMetadataForRuntime(lease)
}

// GetProviderMetadataForRuntime returns provider facts from one retained
// request generation. The caller owns the lease lifecycle.
func (r *Registry) GetProviderMetadataForRuntime(
	lease connectors.RuntimeLease,
) []ProviderMetadata {
	if lease == nil {
		return nil
	}
	snapshot := lease.Snapshot()
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
		if generationLease, ok := lease.(*Lease); ok {
			entry := generationLease.generation.providers[string(route.ProviderID)]
			item.RequiresAuth = entry.registration.RequiresAuth
		}
		seen[route.ProviderID] = struct{}{}
		metadata = append(metadata, item)
	}
	return metadata
}

// Snapshot returns one complete current runtime snapshot.
func (r *Registry) Snapshot() *runtimecatalog.RoutableSnapshot {
	lease, err := r.AcquireRuntime()
	if err != nil {
		return nil
	}
	defer lease.Release()
	return lease.Snapshot()
}

func (r *Registry) recordDrainError(err error) {
	if err == nil {
		return
	}
	r.drainMu.Lock()
	r.drainErrors = append(r.drainErrors, err)
	r.drainMu.Unlock()
}

func (r *Registry) catalogSnapshot() *runtimecatalog.RoutableSnapshot {
	if r == nil || r.catalog == nil {
		return nil
	}
	return r.catalog.Current()
}
