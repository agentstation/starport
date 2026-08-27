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
	// ErrProviderRequired reports a registration without a provider ID.
	ErrProviderRequired = errors.New("provider registration name is required")
	// ErrConnectorRequired reports a registration without a connector.
	ErrConnectorRequired = errors.New("provider connector is required")
	// ErrRegistryStarted reports a second start request.
	ErrRegistryStarted = errors.New("registry already started")
	// ErrRegistryClosed reports an operation after registry shutdown.
	ErrRegistryClosed = errors.New("registry is closed")
)

// Registration binds one compiled connector and optional operator state to a
// provider ID.
type Registration struct {
	Provider        string
	Connector       connectors.Connector
	Operations      []starmapcatalogs.ProviderOperation
	EndpointTypes   []starmapcatalogs.EndpointType
	OperatorBaseURL string
	RequiresAuth    bool
	OperatorSource  credentials.MaterialSource
	Anonymous       credentials.Material
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

// Open creates a registry from one complete executable provider set.
func Open(catalogPlane *runtimecatalog.ControlPlane, registrations []Registration) (*Registry, error) {
	if catalogPlane == nil {
		return nil, ErrCatalogRequired
	}
	registry := NewEmptyWithCatalog(catalogPlane)
	candidate, err := prepareCandidate(registrations)
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

// Register adds a connector to the registry. It derives no operation and no
// endpoint type. Both come from the compiled transport registry through
// providers.Assess, which is the one seam that intersects a catalog offering
// with what this build can execute. A second derivation here read the catalog
// alone, so it published operations no transport implements.
func (r *Registry) Register(provider string, connector connectors.Connector) error {
	if provider == "" {
		return ErrProviderRequired
	}
	if connector == nil {
		return fmt.Errorf("%s: %w", provider, ErrConnectorRequired)
	}
	registration := Registration{Provider: provider, Connector: connector}
	if snapshot := r.catalogSnapshot(); snapshot != nil {
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
	candidate, err := prepareCandidate(registrations)
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
		return credentials.Material{}, connectors.ErrRuntimeUnavailable
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
