package catalog

import (
	"errors"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// ErrDisclosurePolicyRequired reports a missing viewer disclosure policy.
var ErrDisclosurePolicyRequired = errors.New("catalog disclosure policy is required")

// DisclosurePolicy selects accepted facts that a viewer can inspect.
// Implementations retain one immutable policy revision and read only memory.
// Disclosure permission does not authorize inference.
type DisclosurePolicy interface {
	AllowsDefinition(catalogs.ModelDefinitionID) bool
	AllowsOffering(catalogs.OfferingKey) bool
}

// Discovery contains caller-owned facts from one accepted catalog generation.
// Adapter availability and credential state do not determine this membership.
type Discovery struct {
	GenerationID    string
	PayloadChecksum string
	Models          []DiscoveredModel
}

// DiscoveredModel contains one permitted definition and its permitted offerings.
// These facts do not assert structural support or caller readiness.
type DiscoveredModel struct {
	Definition catalogs.ModelDefinition
	Offerings  []catalogs.ProviderOffering
}

// Discover applies explicit disclosure policy to this accepted generation.
// It checks authority before and after projection without external reads.
// The caller must recheck current disclosure policy before response delivery.
func (s *RoutableSnapshot) Discover(policy DisclosurePolicy) (Discovery, error) {
	if policy == nil {
		return Discovery{}, ErrDisclosurePolicyRequired
	}
	if refusal := s.CheckNewAttempt(); refusal != nil {
		return Discovery{}, refusal
	}
	result := Discovery{
		GenerationID:    s.generationID,
		PayloadChecksum: s.payloadChecksum,
		Models:          make([]DiscoveredModel, 0),
	}
	for _, definition := range s.catalog.Definitions() {
		if !policy.AllowsDefinition(definition.ID) {
			continue
		}
		offerings, err := s.catalog.DefinitionOfferings(definition.ID)
		if err != nil {
			return Discovery{}, err
		}
		model := DiscoveredModel{Definition: definition, Offerings: make([]catalogs.ProviderOffering, 0, len(offerings))}
		for _, offering := range offerings {
			key := catalogs.OfferingKey{ProviderID: offering.ProviderID, ProviderModelID: offering.ProviderModelID}
			if policy.AllowsOffering(key) {
				model.Offerings = append(model.Offerings, offering)
			}
		}
		result.Models = append(result.Models, model)
	}
	if refusal := s.CheckNewAttempt(); refusal != nil {
		return Discovery{}, refusal
	}
	return result, nil
}
