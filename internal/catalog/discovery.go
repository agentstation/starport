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
	for _, entry := range s.discoveryIndex {
		if !policy.AllowsDefinition(entry.definition) {
			continue
		}
		definition, err := s.catalog.Definition(entry.definition)
		if err != nil {
			return Discovery{}, err
		}
		model := DiscoveredModel{Definition: definition, Offerings: make([]catalogs.ProviderOffering, 0)}
		for _, key := range entry.offerings {
			if !policy.AllowsOffering(key) {
				continue
			}
			offering, err := s.catalog.Offering(key.ProviderID, key.ProviderModelID)
			if err != nil {
				return Discovery{}, err
			}
			model.Offerings = append(model.Offerings, offering)
		}
		result.Models = append(result.Models, model)
	}

	if refusal := s.CheckNewAttempt(); refusal != nil {
		return Discovery{}, refusal
	}
	return result, nil
}

type discoveryEntry struct {
	definition catalogs.ModelDefinitionID
	offerings  []catalogs.OfferingKey
}

func buildDiscoveryIndex(source *catalogs.Catalog) ([]discoveryEntry, error) {
	if source == nil {
		return nil, nil
	}
	definitions := source.Definitions()
	entries := make([]discoveryEntry, 0, len(definitions))
	for _, definition := range definitions {
		offerings, err := source.DefinitionOfferings(definition.ID)
		if err != nil {
			return nil, err
		}
		entry := discoveryEntry{definition: definition.ID, offerings: make([]catalogs.OfferingKey, len(offerings))}
		for i, offering := range offerings {
			entry.offerings[i] = offering.Key()
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
