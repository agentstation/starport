package providers

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers/connectors"
)

var (
	// ErrProviderMissingCatalog reports configured runtime state without an
	// exact catalog provider.
	ErrProviderMissingCatalog = errors.New("configured provider is missing from Starmap")
	// ErrProviderMissingOffering reports a configured provider without one
	// routable offering supported by compiled primitives.
	ErrProviderMissingOffering = errors.New("configured provider has no supported Starmap offering")
	// ErrProviderConfigurationInvalid reports invalid operational settings or
	// endpoint bindings.
	ErrProviderConfigurationInvalid = errors.New("provider runtime configuration is invalid")
)

// Activation is the intersection of one configured catalog provider and
// Starport's compiled transport and authentication primitives.
type Activation struct {
	ProviderID    catalogs.ProviderID
	Configuration Configuration
	Operations    []catalogs.ProviderOperation
	EndpointTypes []catalogs.EndpointType
	RequiresAuth  bool
}

// Activate derives runtime providers from catalog facts. A provider ID labels
// the activation but never selects compiled behavior.
func Activate(
	catalog *catalogs.Catalog,
	transports *connectors.TransportRegistry,
	authentication *providerauth.Registry,
	configurations map[catalogs.ProviderID]Configuration,
) ([]Activation, error) {
	if catalog == nil {
		return nil, errors.New("catalog is required")
	}
	if transports == nil {
		return nil, errors.New("provider inference transport registry is required")
	}
	if authentication == nil {
		return nil, errors.New("provider authentication registry is required")
	}
	providerIDs := make([]catalogs.ProviderID, 0, len(configurations))
	for providerID := range configurations {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Slice(providerIDs, func(left, right int) bool { return providerIDs[left] < providerIDs[right] })

	activations := make([]Activation, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		configured := configurations[providerID]
		provider, err := catalog.Provider(providerID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", providerID, ErrProviderMissingCatalog)
		}
		if configured.Profile.ID == "" {
			return nil, fmt.Errorf("%s: credential profile is required: %w", providerID, ErrProviderConfigurationInvalid)
		}
		declaredProfile, err := inferenceProfile(provider, configured.Profile.ID)
		if err != nil || !reflect.DeepEqual(declaredProfile, configured.Profile) {
			return nil, fmt.Errorf("%s: selected credential profile does not match Starmap: %w", providerID, ErrProviderConfigurationInvalid)
		}
		if !authentication.Supports(declaredProfile.Primitive) {
			return nil, fmt.Errorf("%s authentication %s: %w", providerID, declaredProfile.Primitive, providerauth.ErrPrimitiveUnsupported)
		}
		if configured.CredentialSource == nil {
			return nil, fmt.Errorf("%s: credential material source is required: %w", providerID, ErrProviderConfigurationInvalid)
		}
		if provider.Inference == nil {
			return nil, fmt.Errorf("%s: Starmap inference service is required: %w", providerID, ErrProviderConfigurationInvalid)
		}
		baseURL := strings.TrimSpace(configured.Connector.BaseURL)
		if baseURL == "" {
			baseURL = strings.TrimSpace(provider.Inference.BaseURL)
		}
		if baseURL == "" {
			return nil, fmt.Errorf("%s: Starmap inference base URL is required: %w", providerID, ErrProviderConfigurationInvalid)
		}
		configured.Connector.BaseURL = strings.TrimRight(baseURL, "/")

		offerings, err := catalog.ProviderOfferings(providerID)
		if err != nil {
			return nil, fmt.Errorf("%s: read offerings: %w", providerID, err)
		}
		operations := make(map[catalogs.ProviderOperation]struct{})
		endpointTypes := make(map[catalogs.EndpointType]struct{})
		var firstUnsupported error
		for _, offering := range offerings {
			if offering.Availability == catalogs.OfferingAvailabilityUnavailable ||
				offering.Lifecycle == catalogs.OfferingLifecycleRetired {
				continue
			}
			for _, operation := range offering.Service.Operations {
				endpoint, found := offering.Endpoint(operation)
				if !found {
					continue
				}
				if !transports.Supports(endpoint.Type, operation) {
					if firstUnsupported == nil {
						firstUnsupported = fmt.Errorf("%s %s: %w", endpoint.Type, operation, connectors.ErrTransportOperationUnsupported)
					}
					continue
				}
				if _, err := provider.Inference.BindOfferingEndpoint(
					endpoint,
					configured.Connector.BaseURL,
					configured.Connector.EndpointBindings,
				); err != nil {
					return nil, fmt.Errorf("%s: %w: %v", providerID, ErrProviderConfigurationInvalid, err)
				}
				operations[operation] = struct{}{}
				endpointTypes[endpoint.Type] = struct{}{}
			}
		}
		if len(endpointTypes) == 0 {
			if firstUnsupported != nil {
				return nil, fmt.Errorf("%s: %w", providerID, firstUnsupported)
			}
			return nil, fmt.Errorf("%s: %w", providerID, ErrProviderMissingOffering)
		}

		activation := Activation{
			ProviderID:    providerID,
			Configuration: configured,
			RequiresAuth:  configured.Profile.Primitive != catalogs.ProviderAuthenticationNone,
		}
		for operation := range operations {
			activation.Operations = append(activation.Operations, operation)
		}
		for endpointType := range endpointTypes {
			activation.EndpointTypes = append(activation.EndpointTypes, endpointType)
		}
		sort.Slice(activation.Operations, func(left, right int) bool {
			return activation.Operations[left] < activation.Operations[right]
		})
		sort.Slice(activation.EndpointTypes, func(left, right int) bool {
			return activation.EndpointTypes[left] < activation.EndpointTypes[right]
		})
		activations = append(activations, activation)
	}
	return activations, nil
}

func inferenceProfile(
	provider catalogs.Provider,
	profileID catalogs.ProviderCredentialProfileID,
) (catalogs.ProviderCredentialProfile, error) {
	if provider.Credentials == nil {
		return catalogs.ProviderCredentialProfile{}, errors.New("provider has no credential contract")
	}
	allowed := false
	for _, candidate := range provider.Credentials.Inference.Alternatives {
		if candidate == profileID {
			allowed = true
			break
		}
	}
	if !allowed {
		return catalogs.ProviderCredentialProfile{}, errors.New("profile is not allowed for inference")
	}
	for _, profile := range provider.Credentials.Profiles {
		if profile.ID == profileID {
			return profile, nil
		}
	}
	return catalogs.ProviderCredentialProfile{}, errors.New("profile is not declared")
}
