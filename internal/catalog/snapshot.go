package catalog

import (
	"strings"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
)

// Route is one immutable, generation-bound provider offering identity.
type Route struct {
	CatalogGenerationID string
	DefinitionID        catalogs.ModelDefinitionID
	ProviderID          catalogs.ProviderID
	ProviderModelID     catalogs.ProviderModelID
	Operations          []catalogs.ProviderOperation
	Endpoints           []catalogs.ProviderOfferingEndpoint
	PromptCache         *bool
}

// ID returns Starport's provider-scoped route ID.
func (r Route) ID() string {
	return string(r.ProviderID) + "/" + string(r.ProviderModelID)
}

// Key returns the exact Starmap provider offering identity.
func (r Route) Key() catalogs.OfferingKey {
	return catalogs.OfferingKey{
		ProviderID:      r.ProviderID,
		ProviderModelID: r.ProviderModelID,
	}
}

// Supports reports whether the catalog offering and compiled adapter both
// support the operation.
func (r Route) Supports(operation catalogs.ProviderOperation) bool {
	for _, supported := range r.Operations {
		if supported == operation {
			return true
		}
	}
	return false
}

// Endpoint returns the exact Starmap endpoint for a supported operation.
func (r Route) Endpoint(operation catalogs.ProviderOperation) (catalogs.ProviderOfferingEndpoint, bool) {
	for _, endpoint := range r.Endpoints {
		if endpoint.Operation == operation {
			return endpoint, true
		}
	}
	return catalogs.ProviderOfferingEndpoint{}, false
}

// SupportsPromptCache reports exact offering support. Unknown is not support.
func (r Route) SupportsPromptCache() bool {
	return r.PromptCache != nil && *r.PromptCache
}

// RouteExclusion names the derivation filter that kept one catalog offering out
// of the routable set. It is the route planner's own vocabulary. A caller that
// reports operator state maps it into its own reason words.
type RouteExclusion string

// Route exclusions are a closed set, ordered the way the derivation applies
// them. An offering carries the first exclusion that rejected it.
const (
	// RouteExclusionNone marks an offering that planning kept.
	RouteExclusionNone RouteExclusion = ""
	// RouteExclusionAdapterNotReady reports that the provider has no adapter
	// able to carry a request, so none of its offerings can be reached.
	RouteExclusionAdapterNotReady RouteExclusion = "adapter_not_ready"
	// RouteExclusionCatalogRetired reports a retired offering lifecycle.
	RouteExclusionCatalogRetired RouteExclusion = "catalog_retired"
	// RouteExclusionCatalogUnavailable reports catalog-declared unavailability.
	RouteExclusionCatalogUnavailable RouteExclusion = "catalog_unavailable"
	// RouteExclusionOfferingUnavailable reports that the availability owner
	// currently withholds this offering.
	RouteExclusionOfferingUnavailable RouteExclusion = "offering_unavailable"
	// RouteExclusionOperationUnsupported reports that the offering and the
	// adapter share no operation with a usable endpoint. The offering exists in
	// the catalog and is healthy, and no request can reach it.
	RouteExclusionOperationUnsupported RouteExclusion = "operation_unsupported"
	// RouteExclusionOperationUnpriced reports that every operation the offering
	// and the adapter share is one the gateway cannot bill. It is a separate
	// verdict from operation_unsupported because the fix is a catalog price,
	// not a compiled adapter.
	RouteExclusionOperationUnpriced RouteExclusion = "operation_unpriced"
)

// OfferingRoutability is the planning verdict for one exact catalog offering.
// The verdict set is total: every offering in the generation carries one, so a
// caller can tell an advertised offering apart from a reachable one.
type OfferingRoutability struct {
	ProviderID      catalogs.ProviderID
	ProviderModelID catalogs.ProviderModelID
	Routable        bool
	Exclusion       RouteExclusion
}

// RoutableSnapshot projects one Starmap generation and one runtime availability
// revision into an immutable route set.
type RoutableSnapshot struct {
	catalog              *catalogs.Catalog
	generationID         string
	payloadChecksum      string
	generatedAt          time.Time
	catalogSequence      uint64
	availabilityRevision uint64
	routes               []Route
	routability          []OfferingRoutability
}

func newRoutableSnapshot(
	state starmap.CatalogState,
	availabilityRevision uint64,
	routes []Route,
	routability []OfferingRoutability,
) *RoutableSnapshot {
	return &RoutableSnapshot{
		catalog:              state.Catalog,
		generationID:         state.GenerationID,
		payloadChecksum:      state.PayloadChecksum,
		generatedAt:          state.GeneratedAt,
		catalogSequence:      state.Sequence,
		availabilityRevision: availabilityRevision,
		routes:               cloneRoutes(routes),
		routability:          append([]OfferingRoutability(nil), routability...),
	}
}

// OfferingRoutability returns the planning verdict for every offering in the
// generation, routable or not, sorted by provider and provider model ID.
func (s *RoutableSnapshot) OfferingRoutability() []OfferingRoutability {
	if s == nil {
		return nil
	}
	return append([]OfferingRoutability(nil), s.routability...)
}

// GenerationID returns the Starmap generation used to derive this snapshot.
func (s *RoutableSnapshot) GenerationID() string {
	if s == nil {
		return ""
	}
	return s.generationID
}

// PayloadChecksum returns the checksum bound to the Starmap generation.
func (s *RoutableSnapshot) PayloadChecksum() string {
	if s == nil {
		return ""
	}
	return s.payloadChecksum
}

// GeneratedAt returns the Starmap generation timestamp.
func (s *RoutableSnapshot) GeneratedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.generatedAt
}

// CatalogSequence returns the source's monotonic generation sequence.
func (s *RoutableSnapshot) CatalogSequence() uint64 {
	if s == nil {
		return 0
	}
	return s.catalogSequence
}

// AvailabilityRevision returns the runtime availability revision.
func (s *RoutableSnapshot) AvailabilityRevision() uint64 {
	if s == nil {
		return 0
	}
	return s.availabilityRevision
}

// Catalog returns the retained immutable Starmap catalog. Starmap guarantees
// that published catalogs are safe to share and retain across goroutines.
func (s *RoutableSnapshot) Catalog() *catalogs.Catalog {
	if s == nil {
		return nil
	}
	return s.catalog
}

// Routes returns a caller-owned copy of the routable offering identities.
func (s *RoutableSnapshot) Routes() []Route {
	if s == nil {
		return nil
	}
	return cloneRoutes(s.routes)
}

// RoutesForProvider returns routable offerings for one provider in stable order.
func (s *RoutableSnapshot) RoutesForProvider(providerID catalogs.ProviderID) []Route {
	if s == nil {
		return nil
	}
	routes := make([]Route, 0)
	for _, route := range s.routes {
		if route.ProviderID == providerID {
			routes = append(routes, cloneRoute(route))
		}
	}
	return routes
}

// RoutesForDefinition returns routable offerings for one canonical model.
func (s *RoutableSnapshot) RoutesForDefinition(definitionID catalogs.ModelDefinitionID) []Route {
	if s == nil {
		return nil
	}
	routes := make([]Route, 0)
	for _, route := range s.routes {
		if route.DefinitionID == definitionID {
			routes = append(routes, cloneRoute(route))
		}
	}
	return routes
}

// Definitions returns caller-owned Starmap definitions that have a routable offering.
func (s *RoutableSnapshot) Definitions() []catalogs.ModelDefinition {
	if s == nil || s.catalog == nil {
		return nil
	}
	definitions := make([]catalogs.ModelDefinition, 0)
	seen := make(map[catalogs.ModelDefinitionID]struct{})
	for _, route := range s.routes {
		if _, exists := seen[route.DefinitionID]; exists {
			continue
		}
		definition, err := s.catalog.Definition(route.DefinitionID)
		if err != nil {
			continue
		}
		seen[route.DefinitionID] = struct{}{}
		definitions = append(definitions, definition)
	}
	return definitions
}

// ResolveRoute resolves a provider-scoped route ID or a canonical definition
// ID to the first stable routable offering.
func (s *RoutableSnapshot) ResolveRoute(modelID string) (Route, bool) {
	if s == nil || strings.TrimSpace(modelID) == "" {
		return Route{}, false
	}
	for _, route := range s.routes {
		if route.ID() == modelID {
			return cloneRoute(route), true
		}
	}
	for _, route := range s.routes {
		if string(route.DefinitionID) == modelID {
			return cloneRoute(route), true
		}
	}
	return Route{}, false
}

// ResolveOperation resolves only routes that support one exact operation.
func (s *RoutableSnapshot) ResolveOperation(
	modelID string,
	operation catalogs.ProviderOperation,
) (Route, bool) {
	if s == nil || strings.TrimSpace(modelID) == "" {
		return Route{}, false
	}
	for _, route := range s.routes {
		if route.ID() == modelID && route.Supports(operation) {
			return cloneRoute(route), true
		}
	}
	for _, route := range s.routes {
		if string(route.DefinitionID) == modelID && route.Supports(operation) {
			return cloneRoute(route), true
		}
	}
	return Route{}, false
}

// Definition returns one caller-owned definition from this exact generation.
func (s *RoutableSnapshot) Definition(
	id catalogs.ModelDefinitionID,
) (catalogs.ModelDefinition, error) {
	if s == nil || s.catalog == nil {
		return catalogs.ModelDefinition{}, ErrCatalogRequired
	}
	return s.catalog.Definition(id)
}

// Offering returns one caller-owned offering from this exact generation.
func (s *RoutableSnapshot) Offering(route Route) (catalogs.ProviderOffering, error) {
	if s == nil || s.catalog == nil {
		return catalogs.ProviderOffering{}, ErrCatalogRequired
	}
	return s.catalog.Offering(route.ProviderID, route.ProviderModelID)
}

// PagePriceFor returns what one model charges to read one page of a document,
// in USD. A page is the unit recognition is billed in, and no token price
// converts into it.
func (s *RoutableSnapshot) PagePriceFor(
	modelID string,
	operation catalogs.ProviderOperation,
) (float64, bool) {
	route, found := s.ResolveOperation(modelID, operation)
	if !found {
		return 0, false
	}
	return s.routePagePrice(route)
}

// LowestPagePrice returns the cheapest page price this generation publishes for
// one operation.
//
// It answers the question a caller has before a route exists: what is the least
// this document can cost to read? The planner picks the offering afterwards, so
// no exact price is knowable yet, and the cheapest one is the only bound that
// refuses no work the account could have paid for.
func (s *RoutableSnapshot) LowestPagePrice(operation catalogs.ProviderOperation) (float64, bool) {
	if s == nil {
		return 0, false
	}
	lowest, found := 0.0, false
	for _, route := range s.routes {
		if !route.Supports(operation) {
			continue
		}
		price, priced := s.routePagePrice(route)
		if !priced {
			continue
		}
		if !found || price < lowest {
			lowest, found = price, true
		}
	}
	return lowest, found
}

// routePagePrice reads the per-page price one route's offering publishes. The
// projection refuses a recognition offering that publishes none, so an offering
// this gateway can route always answers.
func (s *RoutableSnapshot) routePagePrice(route Route) (float64, bool) {
	offering, err := s.Offering(route)
	if err != nil {
		return 0, false
	}
	if offering.Pricing == nil || offering.Pricing.Operations == nil {
		return 0, false
	}
	page := offering.Pricing.Operations.PageInput
	if page == nil || *page < 0 {
		return 0, false
	}
	return *page, true
}

func cloneRoutes(source []Route) []Route {
	result := make([]Route, len(source))
	for index, route := range source {
		result[index] = cloneRoute(route)
	}
	return result
}

func cloneRoute(route Route) Route {
	copyRoute := route
	copyRoute.Operations = append([]catalogs.ProviderOperation(nil), route.Operations...)
	copyRoute.Endpoints = append([]catalogs.ProviderOfferingEndpoint(nil), route.Endpoints...)
	if route.PromptCache != nil {
		value := *route.PromptCache
		copyRoute.PromptCache = &value
	}
	return copyRoute
}
