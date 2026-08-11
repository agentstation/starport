// Package catalog owns Starport's immutable view of Starmap facts and the
// separately versioned runtime availability used to derive routable models.
package catalog

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/availability"
)

var (
	// ErrCatalogSourceRequired reports a missing Starmap catalog source.
	ErrCatalogSourceRequired = errors.New("catalog source is required")
	// ErrCatalogRequired means that a Starmap state has no immutable catalog.
	ErrCatalogRequired = errors.New("catalog state must contain a catalog")
	// ErrCatalogGenerationRequired means that a Starmap state has no generation identity.
	ErrCatalogGenerationRequired = errors.New("catalog state must contain a generation ID")
)

// Source supplies one atomic Starmap catalog and generation pair.
type Source interface {
	CurrentCatalogState() starmap.CatalogState
}

// AdapterAvailability is runtime state for one compiled provider adapter. It
// is not a catalog fact and does not contain operator credential state.
type AdapterAvailability struct {
	ProviderID    catalogs.ProviderID
	Registered    bool
	Operations    []catalogs.ProviderOperation
	EndpointTypes []catalogs.EndpointType
}

func (a AdapterAvailability) routable() bool {
	return a.Registered
}

// ControlPlane atomically publishes one routable view derived from an immutable
// Starmap generation and separately versioned runtime availability.
type ControlPlane struct {
	source Source

	mu                         sync.Mutex
	state                      starmap.CatalogState
	availabilityRevision       uint64
	availabilitySourceRevision uint64
	adapters                   map[catalogs.ProviderID]AdapterAvailability
	unavailableOfferings       map[catalogs.OfferingKey]struct{}
	current                    atomic.Pointer[RoutableSnapshot]
}

// Open creates the catalog control plane from the source's current atomic state.
func Open(source Source) (*ControlPlane, error) {
	if source == nil {
		return nil, ErrCatalogSourceRequired
	}

	plane := &ControlPlane{
		source:               source,
		adapters:             make(map[catalogs.ProviderID]AdapterAvailability),
		unavailableOfferings: make(map[catalogs.OfferingKey]struct{}),
	}
	if err := plane.Activate(source.CurrentCatalogState()); err != nil {
		return nil, err
	}
	return plane, nil
}

// Refresh atomically activates the source's current catalog generation.
func (p *ControlPlane) Refresh() error {
	if p == nil || p.source == nil {
		return ErrCatalogSourceRequired
	}
	return p.Activate(p.source.CurrentCatalogState())
}

// Activate validates and atomically publishes one complete catalog generation.
// Retained older snapshots remain valid and do not observe the new generation.
func (p *ControlPlane) Activate(state starmap.CatalogState) error {
	if p == nil {
		return ErrCatalogSourceRequired
	}
	if err := validateCatalogState(state); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	snapshot, err := deriveRoutableSnapshot(
		state,
		p.availabilityRevision,
		p.adapters,
		p.unavailableOfferings,
	)
	if err != nil {
		return err
	}
	p.state = state
	p.current.Store(snapshot)
	return nil
}

// ReplaceAdapters replaces the complete runtime adapter set in one publication.
func (p *ControlPlane) ReplaceAdapters(adapters []AdapterAvailability) error {
	if p == nil {
		return ErrCatalogSourceRequired
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	next, err := normalizeAdapters(p.state.Catalog, adapters)
	if err != nil {
		return err
	}

	return p.publishAvailabilityLocked(
		next,
		p.unavailableOfferings,
	)
}

// ValidateRuntime proves that one catalog state and complete adapter set can
// produce a routable snapshot without changing published state.
func (p *ControlPlane) ValidateRuntime(
	state starmap.CatalogState,
	adapters []AdapterAvailability,
) error {
	if p == nil {
		return ErrCatalogSourceRequired
	}
	if err := validateCatalogState(state); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next, err := normalizeAdapters(state.Catalog, adapters)
	if err != nil {
		return err
	}
	_, err = deriveRoutableSnapshot(
		state,
		p.availabilityRevision+1,
		next,
		p.unavailableOfferings,
	)
	return err
}

// ReplaceRuntime atomically publishes one catalog state and complete adapter
// set. Retained snapshots remain immutable.
func (p *ControlPlane) ReplaceRuntime(
	state starmap.CatalogState,
	adapters []AdapterAvailability,
) (*RoutableSnapshot, error) {
	if p == nil {
		return nil, ErrCatalogSourceRequired
	}
	if err := validateCatalogState(state); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next, err := normalizeAdapters(state.Catalog, adapters)
	if err != nil {
		return nil, err
	}
	nextRevision := p.availabilityRevision + 1
	snapshot, err := deriveRoutableSnapshot(
		state,
		nextRevision,
		next,
		p.unavailableOfferings,
	)
	if err != nil {
		return nil, err
	}
	p.state = state
	p.availabilityRevision = nextRevision
	p.adapters = cloneAdapters(next)
	p.current.Store(snapshot)
	return snapshot, nil
}

// SetAdapter updates one runtime adapter and atomically republishes the derived view.
func (p *ControlPlane) SetAdapter(adapter AdapterAvailability) error {
	if p == nil {
		return ErrCatalogSourceRequired
	}
	if strings.TrimSpace(string(adapter.ProviderID)) == "" {
		return fmt.Errorf("adapter provider ID is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	adapter.ProviderID = canonicalProviderID(p.state.Catalog, adapter.ProviderID)
	next := cloneAdapters(p.adapters)
	next[adapter.ProviderID] = adapter
	return p.publishAvailabilityLocked(
		next,
		p.unavailableOfferings,
	)
}

// RemoveAdapter removes one runtime adapter and atomically republishes the view.
func (p *ControlPlane) RemoveAdapter(providerID catalogs.ProviderID) error {
	if p == nil {
		return ErrCatalogSourceRequired
	}
	if strings.TrimSpace(string(providerID)) == "" {
		return fmt.Errorf("adapter provider ID is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	providerID = canonicalProviderID(p.state.Catalog, providerID)
	next := cloneAdapters(p.adapters)
	delete(next, providerID)
	return p.publishAvailabilityLocked(
		next,
		p.unavailableOfferings,
	)
}

// PublishAvailability applies one availability-owner generation to the derived
// routable projection. It does not own availability state transitions.
func (p *ControlPlane) PublishAvailability(snapshot availability.Snapshot) error {
	if p == nil {
		return ErrCatalogSourceRequired
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if snapshot.Revision <= p.availabilitySourceRevision {
		return nil
	}

	next := make(map[catalogs.OfferingKey]struct{})
	for _, record := range snapshot.Records {
		if record.State != availability.StateOpen && record.State != availability.StateUnavailable {
			continue
		}
		key := catalogs.OfferingKey{
			ProviderID:      catalogs.ProviderID(record.Offering.ProviderID),
			ProviderModelID: catalogs.ProviderModelID(record.Offering.ProviderModelID),
		}
		if strings.TrimSpace(string(key.ProviderID)) == "" || strings.TrimSpace(string(key.ProviderModelID)) == "" {
			return fmt.Errorf("offering provider and model IDs are required")
		}
		key.ProviderID = canonicalProviderID(p.state.Catalog, key.ProviderID)
		next[key] = struct{}{}
	}
	if err := p.publishAvailabilityLocked(p.adapters, next); err != nil {
		return err
	}
	p.availabilitySourceRevision = snapshot.Revision
	return nil
}

// Current returns the current immutable routable snapshot in O(1).
func (p *ControlPlane) Current() *RoutableSnapshot {
	if p == nil {
		return nil
	}
	return p.current.Load()
}

func (p *ControlPlane) publishAvailabilityLocked(
	adapters map[catalogs.ProviderID]AdapterAvailability,
	unavailable map[catalogs.OfferingKey]struct{},
) error {
	nextRevision := p.availabilityRevision + 1
	snapshot, err := deriveRoutableSnapshot(
		p.state,
		nextRevision,
		adapters,
		unavailable,
	)
	if err != nil {
		return err
	}
	p.availabilityRevision = nextRevision
	p.adapters = cloneAdapters(adapters)
	p.unavailableOfferings = cloneUnavailableOfferings(unavailable)
	p.current.Store(snapshot)
	return nil
}

func validateCatalogState(state starmap.CatalogState) error {
	if state.Catalog == nil {
		return ErrCatalogRequired
	}
	if strings.TrimSpace(state.GenerationID) == "" {
		return ErrCatalogGenerationRequired
	}
	return nil
}

func deriveRoutableSnapshot(
	state starmap.CatalogState,
	availabilityRevision uint64,
	adapters map[catalogs.ProviderID]AdapterAvailability,
	unavailable map[catalogs.OfferingKey]struct{},
) (*RoutableSnapshot, error) {
	if err := validateCatalogState(state); err != nil {
		return nil, err
	}

	routes := make([]Route, 0)
	for _, provider := range state.Catalog.Providers().List() {
		adapter, exists := adapters[provider.ID]
		if !exists || !adapter.routable() {
			continue
		}

		offerings, err := state.Catalog.ProviderOfferings(provider.ID)
		if err != nil {
			return nil, fmt.Errorf("read Starmap offerings for %s: %w", provider.ID, err)
		}
		for _, offering := range offerings {
			if !catalogOfferingRoutable(offering) {
				continue
			}
			if _, blocked := unavailable[offering.Key()]; blocked {
				continue
			}
			operations, endpoints := compatibleOfferingService(adapter, offering)
			if len(operations) == 0 {
				continue
			}
			routes = append(routes, Route{
				CatalogGenerationID: state.GenerationID,
				DefinitionID:        offering.DefinitionID,
				ProviderID:          offering.ProviderID,
				ProviderModelID:     offering.ProviderModelID,
				Operations:          operations,
				Endpoints:           endpoints,
				PromptCache:         copyBool(offering.Service.PromptCache),
			})
		}
	}

	sort.Slice(routes, func(left, right int) bool {
		if routes[left].DefinitionID != routes[right].DefinitionID {
			return routes[left].DefinitionID < routes[right].DefinitionID
		}
		if routes[left].ProviderID != routes[right].ProviderID {
			return routes[left].ProviderID < routes[right].ProviderID
		}
		return routes[left].ProviderModelID < routes[right].ProviderModelID
	})

	return newRoutableSnapshot(state, availabilityRevision, routes), nil
}

func compatibleOfferingService(
	adapter AdapterAvailability,
	offering catalogs.ProviderOffering,
) ([]catalogs.ProviderOperation, []catalogs.ProviderOfferingEndpoint) {
	operations := make([]catalogs.ProviderOperation, 0, len(offering.Service.Operations))
	endpoints := make([]catalogs.ProviderOfferingEndpoint, 0, len(offering.Endpoints))
	for _, operation := range offering.Service.Operations {
		if !containsOperation(adapter.Operations, operation) {
			continue
		}
		endpoint, found := offering.Endpoint(operation)
		if !found || !containsEndpointType(adapter.EndpointTypes, endpoint.Type) {
			continue
		}
		if strings.TrimSpace(endpoint.URL) == "" {
			continue
		}
		operations = append(operations, operation)
		endpoints = append(endpoints, endpoint)
	}
	return operations, endpoints
}

func containsOperation(values []catalogs.ProviderOperation, value catalogs.ProviderOperation) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func containsEndpointType(values []catalogs.EndpointType, value catalogs.EndpointType) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func copyBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func catalogOfferingRoutable(offering catalogs.ProviderOffering) bool {
	if offering.Availability == catalogs.OfferingAvailabilityUnavailable {
		return false
	}
	return offering.Lifecycle != catalogs.OfferingLifecycleRetired
}

func canonicalProviderID(source *catalogs.Catalog, providerID catalogs.ProviderID) catalogs.ProviderID {
	if source == nil {
		return providerID
	}
	provider, err := source.Provider(providerID)
	if err != nil {
		return providerID
	}
	return provider.ID
}

func normalizeAdapters(
	source *catalogs.Catalog,
	adapters []AdapterAvailability,
) (map[catalogs.ProviderID]AdapterAvailability, error) {
	next := make(map[catalogs.ProviderID]AdapterAvailability, len(adapters))
	for _, adapter := range adapters {
		if strings.TrimSpace(string(adapter.ProviderID)) == "" {
			return nil, fmt.Errorf("adapter provider ID is required")
		}
		adapter.ProviderID = canonicalProviderID(source, adapter.ProviderID)
		if _, exists := next[adapter.ProviderID]; exists {
			return nil, fmt.Errorf("adapter provider ID %q is duplicated", adapter.ProviderID)
		}
		adapter.Operations = append([]catalogs.ProviderOperation(nil), adapter.Operations...)
		adapter.EndpointTypes = append([]catalogs.EndpointType(nil), adapter.EndpointTypes...)
		next[adapter.ProviderID] = adapter
	}
	return next, nil
}

func cloneAdapters(source map[catalogs.ProviderID]AdapterAvailability) map[catalogs.ProviderID]AdapterAvailability {
	result := make(map[catalogs.ProviderID]AdapterAvailability, len(source))
	for providerID, adapter := range source {
		adapter.Operations = append([]catalogs.ProviderOperation(nil), adapter.Operations...)
		adapter.EndpointTypes = append([]catalogs.EndpointType(nil), adapter.EndpointTypes...)
		result[providerID] = adapter
	}
	return result
}

func cloneUnavailableOfferings(
	source map[catalogs.OfferingKey]struct{},
) map[catalogs.OfferingKey]struct{} {
	result := make(map[catalogs.OfferingKey]struct{}, len(source))
	for key := range source {
		result[key] = struct{}{}
	}
	return result
}
