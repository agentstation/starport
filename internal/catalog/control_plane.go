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
	// ErrModelNotCatalogued reports a model name the retained generation does
	// not hold. It is separate from an unreachable model because the two have
	// different answers: a name the catalog never held is the caller's to
	// correct, and a catalogued model with no reachable provider is the
	// gateway's to report.
	ErrModelNotCatalogued = errors.New("model is not in the catalog")
	// ErrCatalogGenerationRequired means that a Starmap state has no generation identity.
	ErrCatalogGenerationRequired = errors.New("catalog state must contain a generation ID")
	// ErrMissingPagePrice reports an offering that serves document recognition
	// and states no price per page.
	//
	// Recognition is the one operation whose unit is neither a token nor a
	// request, so a token price says nothing about what a page costs. An
	// offering the gateway cannot price is one it would serve for free against
	// real provider time, and a spend limit set on that tenant would never
	// fire. Planning drops the operation instead of guessing a price.
	ErrMissingPagePrice = errors.New("offering serves document recognition with no page price")
	// ErrRerankUnpriced reports an offering that serves reranking and states
	// no price in the unit it bills.
	//
	// Providers disagree on that unit. Cohere bills a search unit, which is one
	// query against a bounded document count, and Voyage bills the tokens it
	// reads. The offering names its own basis, so an offering that names one
	// and publishes no price for it is a catalog defect rather than a known
	// gap. Planning drops the operation, which keeps a silent zero out of the
	// account's spend total.
	ErrRerankUnpriced = errors.New("offering serves reranking with no price in the unit it bills")
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
	routability := make([]OfferingRoutability, 0)
	for _, provider := range state.Catalog.Providers().List() {
		adapter, exists := adapters[provider.ID]
		adapterReady := exists && adapter.routable()

		offerings, err := state.Catalog.ProviderOfferings(provider.ID)
		if err != nil {
			return nil, fmt.Errorf("read Starmap offerings for %s: %w", provider.ID, err)
		}
		for _, offering := range offerings {
			verdict := OfferingRoutability{
				ProviderID:      provider.ID,
				ProviderModelID: offering.ProviderModelID,
			}
			var operations []catalogs.ProviderOperation
			var endpoints []catalogs.ProviderOfferingEndpoint
			switch {
			case !adapterReady:
				verdict.Exclusion = RouteExclusionAdapterNotReady
			case offering.Lifecycle == catalogs.OfferingLifecycleRetired:
				verdict.Exclusion = RouteExclusionCatalogRetired
			case offering.Availability == catalogs.OfferingAvailabilityUnavailable:
				verdict.Exclusion = RouteExclusionCatalogUnavailable
			default:
				if _, blocked := unavailable[offering.Key()]; blocked {
					verdict.Exclusion = RouteExclusionOfferingUnavailable
					break
				}
				var unpriced bool
				operations, endpoints, unpriced = compatibleOfferingService(adapter, offering)
				if len(operations) == 0 {
					verdict.Exclusion = RouteExclusionOperationUnsupported
					if unpriced {
						verdict.Exclusion = RouteExclusionOperationUnpriced
					}
					break
				}
				verdict.Routable = true
			}
			routability = append(routability, verdict)
			if !verdict.Routable {
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

	sort.Slice(routability, func(left, right int) bool {
		if routability[left].ProviderID != routability[right].ProviderID {
			return routability[left].ProviderID < routability[right].ProviderID
		}
		return routability[left].ProviderModelID < routability[right].ProviderModelID
	})

	sort.Slice(routes, func(left, right int) bool {
		if routes[left].DefinitionID != routes[right].DefinitionID {
			return routes[left].DefinitionID < routes[right].DefinitionID
		}
		if routes[left].ProviderID != routes[right].ProviderID {
			return routes[left].ProviderID < routes[right].ProviderID
		}
		return routes[left].ProviderModelID < routes[right].ProviderModelID
	})

	return newRoutableSnapshot(state, availabilityRevision, routes, routability), nil
}

// compatibleOfferingService names the operations one offering and one adapter
// both serve. It also reports whether an operation was dropped for its price
// alone, which is the one drop an operator fixes in the catalog rather than in
// the build.
func compatibleOfferingService(
	adapter AdapterAvailability,
	offering catalogs.ProviderOffering,
) ([]catalogs.ProviderOperation, []catalogs.ProviderOfferingEndpoint, bool) {
	operations := make([]catalogs.ProviderOperation, 0, len(offering.Service.Operations))
	endpoints := make([]catalogs.ProviderOfferingEndpoint, 0, len(offering.Endpoints))
	unpriced := false
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
		if err := billableOperation(offering, operation); err != nil {
			unpriced = true
			continue
		}
		operations = append(operations, operation)
		endpoints = append(endpoints, endpoint)
	}
	return operations, endpoints, unpriced
}

// billableOperation reports whether the catalog states the price this operation
// is charged in.
//
// Two operations are checked, and the rest are skipped on purpose rather than
// by omission. Every other operation this build plans is billed in tokens or in
// requests, and a token price is already required of any offering the catalog
// publishes. Recognition is billed by the page and reranking by a unit the
// offering itself names, and neither is a unit a token price converts into.
func billableOperation(offering catalogs.ProviderOffering, operation catalogs.ProviderOperation) error {
	switch operation {
	case catalogs.ProviderOperationDocumentsRecognition:
		return billablePages(offering)
	case catalogs.ProviderOperationRerank:
		return billableRerank(offering)
	default:
		return nil
	}
}

// billablePages reports whether the offering states what one page costs.
func billablePages(offering catalogs.ProviderOffering) error {
	if offering.Pricing == nil || offering.Pricing.Operations == nil {
		return fmt.Errorf("%w: %s/%s", ErrMissingPagePrice, offering.ProviderID, offering.ProviderModelID)
	}
	page := offering.Pricing.Operations.PageInput
	if page == nil || *page < 0 {
		return fmt.Errorf("%w: %s/%s", ErrMissingPagePrice, offering.ProviderID, offering.ProviderModelID)
	}
	return nil
}

// billableRerank reports whether the offering states a price in the unit it
// says it bills.
//
// The basis decides which price to read. An offering that states no basis is
// unpriced here even when a token price sits beside it, because the basis
// exists so a consumer reads the right one rather than guessing from whichever
// price happens to be present.
func billableRerank(offering catalogs.ProviderOffering) error {
	unpriced := fmt.Errorf("%w: %s/%s", ErrRerankUnpriced, offering.ProviderID, offering.ProviderModelID)
	if offering.Pricing == nil || offering.Pricing.Operations == nil {
		return unpriced
	}
	switch offering.Pricing.Operations.RerankBasis {
	case catalogs.ModelRerankBasisSearchUnit:
		unit := offering.Pricing.Operations.SearchUnit
		if unit == nil || *unit < 0 {
			return unpriced
		}
		return nil
	case catalogs.ModelRerankBasisToken:
		if offering.Pricing.Tokens == nil || offering.Pricing.Tokens.Input == nil {
			return unpriced
		}
		return nil
	default:
		return unpriced
	}
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
