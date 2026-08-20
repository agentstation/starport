package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
)

type runtimeProvider struct {
	registration Registration
}

type runtimeGeneration struct {
	catalog             *runtimecatalog.ControlPlane
	snapshot            *runtimecatalog.RoutableSnapshot
	catalogGenerationID string
	providers           map[string]runtimeProvider
	onClose             func(error)

	mu       sync.Mutex
	leases   int
	draining bool
	closed   bool
}

// Candidate owns a complete runtime generation until publication or close.
type Candidate struct {
	generation *runtimeGeneration

	mu       sync.Mutex
	consumed bool
}

// Prepare validates and owns a complete replacement generation.
func (r *Registry) Prepare(registrations []Registration) (*Candidate, error) {
	if r == nil {
		return nil, ErrRegistryClosed
	}
	r.lifecycleMu.Lock()
	closed := r.closed
	r.lifecycleMu.Unlock()
	if closed {
		return nil, ErrRegistryClosed
	}
	return prepareCandidate(registrations)
}

func prepareCandidate(registrations []Registration) (*Candidate, error) {
	providers := make(map[string]runtimeProvider, len(registrations))
	for index, registration := range registrations {
		if registration.Provider == "" {
			return nil, closeUnownedRegistrations(ErrProviderRequired, registrations, index)
		}
		if registration.Connector == nil {
			return nil, closeUnownedRegistrations(
				fmt.Errorf("%s: %w", registration.Provider, ErrConnectorRequired),
				registrations,
				index,
			)
		}
		if _, exists := providers[registration.Provider]; exists {
			return nil, closeUnownedRegistrations(
				fmt.Errorf("connector already registered for provider: %s", registration.Provider),
				registrations,
				index,
			)
		}
		registration.Operations = append([]starmapcatalogs.ProviderOperation(nil), registration.Operations...)
		registration.EndpointTypes = append([]starmapcatalogs.EndpointType(nil), registration.EndpointTypes...)
		providers[registration.Provider] = runtimeProvider{registration: registration}
	}
	return &Candidate{generation: &runtimeGeneration{providers: providers}}, nil
}

func closeUnownedRegistrations(cause error, registrations []Registration, start int) error {
	errs := []error{cause}
	for index := len(registrations) - 1; index >= start; index-- {
		registration := registrations[index]
		if registration.Connector == nil {
			continue
		}
		if err := registration.Connector.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close unowned %s connector: %w", registration.Provider, err))
		}
	}
	for index := start - 1; index >= 0; index-- {
		registration := registrations[index]
		if err := registration.Connector.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close prepared %s connector: %w", registration.Provider, err))
		}
	}
	return errors.Join(errs...)
}

// Availability returns the complete adapter projection for catalog validation.
func (c *Candidate) Availability() []runtimecatalog.AdapterAvailability {
	if c == nil || c.generation == nil {
		return nil
	}
	providerIDs := make([]string, 0, len(c.generation.providers))
	for providerID := range c.generation.providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	result := make([]runtimecatalog.AdapterAvailability, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		registration := c.generation.providers[providerID].registration
		result = append(result, runtimecatalog.AdapterAvailability{
			ProviderID: starmapcatalogs.ProviderID(providerID), Registered: true,
			Operations:    append([]starmapcatalogs.ProviderOperation(nil), registration.Operations...),
			EndpointTypes: append([]starmapcatalogs.EndpointType(nil), registration.EndpointTypes...),
		})
	}
	return result
}

// Close releases an unpublished candidate.
func (c *Candidate) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.consumed {
		c.mu.Unlock()
		return nil
	}
	c.consumed = true
	generation := c.generation
	c.mu.Unlock()
	if generation == nil {
		return nil
	}
	return generation.closeProviders()
}

func (c *Candidate) consume() (*runtimeGeneration, error) {
	if c == nil || c.generation == nil {
		return nil, errors.New("runtime generation candidate is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.consumed {
		return nil, errors.New("runtime generation candidate is already consumed")
	}
	c.consumed = true
	return c.generation, nil
}

// Publish atomically installs a validated generation and drains its predecessor.
func (r *Registry) Publish(
	candidate *Candidate,
	snapshot *runtimecatalog.RoutableSnapshot,
) error {
	if r == nil || snapshot == nil {
		return errors.New("runtime generation snapshot is required")
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	generation, err := candidate.consume()
	if err != nil {
		return err
	}
	generation.catalog = r.catalog
	generation.snapshot = snapshot
	generation.catalogGenerationID = snapshot.GenerationID()
	generation.onClose = r.recordDrainError
	previous := r.current.Swap(generation)
	if previous != nil {
		previous.drain()
	}
	return nil
}

// AcquireRuntime retains the current complete generation until Release.
func (r *Registry) AcquireRuntime() (connectors.RuntimeLease, error) {
	if r == nil {
		return nil, ErrRegistryClosed
	}
	for {
		generation := r.current.Load()
		if generation == nil {
			return nil, connectors.ErrRuntimeUnavailable
		}
		if generation.acquire() {
			return &Lease{generation: generation}, nil
		}
	}
}

// Lease retains one complete generation for one request.
type Lease struct {
	generation *runtimeGeneration
	once       sync.Once
}

// Snapshot returns the newest availability view for the leased catalog
// generation. During replacement, it returns the retained complete snapshot.
func (l *Lease) Snapshot() *runtimecatalog.RoutableSnapshot {
	if l == nil || l.generation == nil {
		return nil
	}
	generation := l.generation
	if generation.catalog != nil {
		current := generation.catalog.Current()
		if current != nil && current.GenerationID() == generation.catalogGenerationID {
			return current
		}
	}
	return generation.snapshot
}

// Get returns a connector from the leased generation.
func (l *Lease) Get(provider string) connectors.Connector {
	if l == nil || l.generation == nil {
		return nil
	}
	return l.generation.connector(provider)
}

// RequiresAuthentication reports the retained runtime registration policy.
func (l *Lease) RequiresAuthentication(provider string) bool {
	if l == nil || l.generation == nil {
		return false
	}
	entry, exists := l.generation.providers[provider]
	return exists && entry.registration.RequiresAuth
}

// ResolveMaterial resolves material from the leased generation.
func (l *Lease) ResolveMaterial(
	ctx context.Context,
	provider string,
) (credentials.Material, error) {
	if l == nil || l.generation == nil {
		return credentials.Material{}, connectors.ErrRuntimeUnavailable
	}
	return l.generation.resolveMaterial(ctx, provider)
}

// AnonymousMaterial returns the catalog-default no-auth material, if one is
// executable in this generation.
func (l *Lease) AnonymousMaterial(provider string) (credentials.Material, bool) {
	if l == nil || l.generation == nil {
		return credentials.Material{}, false
	}
	return l.generation.anonymousMaterial(provider)
}

// BindEndpoint applies only the selected request material. Operator endpoint
// overrides are available only when the caller selected operator material.
func (l *Lease) BindEndpoint(
	provider string,
	endpoint starmapcatalogs.ProviderOfferingEndpoint,
	material credentials.Material,
	useOperatorOverride bool,
) (starmapcatalogs.ProviderOfferingEndpoint, error) {
	if l == nil || l.generation == nil {
		return starmapcatalogs.ProviderOfferingEndpoint{}, connectors.ErrRuntimeUnavailable
	}
	return l.generation.bindEndpoint(provider, endpoint, material, useOperatorOverride)
}

// Release ends the request lease. The last old-generation lease closes its
// connectors after replacement.
func (l *Lease) Release() {
	if l == nil || l.generation == nil {
		return
	}
	l.once.Do(l.generation.release)
}

func (g *runtimeGeneration) acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.draining || g.closed {
		return false
	}
	g.leases++
	return true
}

func (g *runtimeGeneration) release() {
	g.mu.Lock()
	if g.leases > 0 {
		g.leases--
	}
	closeNow := g.draining && g.leases == 0 && !g.closed
	if closeNow {
		g.closed = true
	}
	g.mu.Unlock()
	if closeNow {
		g.reportClose(g.closeProviders())
	}
}

func (g *runtimeGeneration) drain() {
	g.mu.Lock()
	g.draining = true
	closeNow := g.leases == 0 && !g.closed
	if closeNow {
		g.closed = true
	}
	g.mu.Unlock()
	if closeNow {
		g.reportClose(g.closeProviders())
	}
}

func (g *runtimeGeneration) reportClose(err error) {
	if err != nil && g.onClose != nil {
		g.onClose(err)
	}
}

func (g *runtimeGeneration) closeProviders() error {
	providerIDs := make([]string, 0, len(g.providers))
	for providerID := range g.providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	var errs []error
	for _, providerID := range providerIDs {
		connector := g.providers[providerID].registration.Connector
		if connector == nil {
			continue
		}
		if err := connector.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s connector: %w", providerID, err))
		}
	}
	return errors.Join(errs...)
}

func (g *runtimeGeneration) connector(provider string) connectors.Connector {
	entry, exists := g.providers[provider]
	if !exists {
		return nil
	}
	return entry.registration.Connector
}

func (g *runtimeGeneration) resolveMaterial(
	ctx context.Context,
	provider string,
) (credentials.Material, error) {
	entry, exists := g.providers[provider]
	if !exists {
		return credentials.Material{}, fmt.Errorf("%s: %w", provider, credentials.ErrProviderNotConfigured)
	}
	if entry.registration.OperatorSource == nil {
		if !entry.registration.Anonymous.Empty() {
			return entry.registration.Anonymous, nil
		}
		return credentials.Material{}, fmt.Errorf("%s: %w", provider, credentials.ErrProviderNotConfigured)
	}
	material, err := entry.registration.OperatorSource.ResolveMaterial(ctx)
	if err != nil {
		return credentials.Material{}, fmt.Errorf("resolve provider %s credential material: %w", provider, err)
	}
	return material, nil
}

func (g *runtimeGeneration) anonymousMaterial(provider string) (credentials.Material, bool) {
	entry, exists := g.providers[provider]
	if !exists || entry.registration.Anonymous.Empty() {
		return credentials.Material{}, false
	}
	return entry.registration.Anonymous, true
}

func (g *runtimeGeneration) bindEndpoint(
	provider string,
	endpoint starmapcatalogs.ProviderOfferingEndpoint,
	material credentials.Material,
	useOperatorOverride bool,
) (starmapcatalogs.ProviderOfferingEndpoint, error) {
	entry, exists := g.providers[provider]
	if !exists || g.snapshot == nil || g.snapshot.Catalog() == nil {
		return starmapcatalogs.ProviderOfferingEndpoint{}, fmt.Errorf("%s: provider runtime is unavailable", provider)
	}
	providerRecord, err := g.snapshot.Catalog().Provider(starmapcatalogs.ProviderID(provider))
	if err != nil || providerRecord.Inference == nil {
		return starmapcatalogs.ProviderOfferingEndpoint{}, fmt.Errorf("%s: provider inference service is unavailable", provider)
	}
	baseURL := ""
	if useOperatorOverride {
		baseURL = entry.registration.OperatorBaseURL
	}
	return providerRecord.Inference.BindOfferingEndpoint(endpoint, baseURL, material.EndpointBindings())
}

func (g *runtimeGeneration) registrations() []Registration {
	providerIDs := make([]string, 0, len(g.providers))
	for providerID := range g.providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	result := make([]Registration, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		result = append(result, g.providers[providerID].registration)
	}
	return result
}
