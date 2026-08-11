package providers

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers/connectors"
)

var (
	// ErrProviderMissingCatalog reports configured runtime state without an
	// exact catalog provider.
	ErrProviderMissingCatalog = errors.New("configured provider is missing from Starmap")
)

// Activation is the intersection of one catalog provider and Starport's
// compiled transport and authentication primitives. Operator state is
// optional.
type Activation struct {
	ProviderID      catalogs.ProviderID
	Configuration   Configuration
	Operations      []catalogs.ProviderOperation
	EndpointTypes   []catalogs.EndpointType
	RequiresAuth    bool
	Anonymous       credentials.Material
	OperatorBaseURL string
}

// Activate derives every executable runtime provider from catalog facts. A
// provider ID labels the activation but never selects compiled behavior.
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
	for providerID := range configurations {
		provider, err := catalog.Provider(providerID)
		if err != nil || provider.ID != providerID {
			return nil, fmt.Errorf("%s: %w", providerID, ErrProviderMissingCatalog)
		}
	}

	providerRecords := catalog.Providers().List()
	providerIDs := make([]catalogs.ProviderID, 0, len(providerRecords))
	providersByID := make(map[catalogs.ProviderID]catalogs.Provider, len(providerRecords))
	for _, provider := range providerRecords {
		providerIDs = append(providerIDs, provider.ID)
		providersByID[provider.ID] = provider
	}
	sort.Slice(providerIDs, func(left, right int) bool { return providerIDs[left] < providerIDs[right] })

	activations := make([]Activation, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		provider := providersByID[providerID]
		if provider.Inference == nil {
			continue
		}
		requiresAuth, anonymous, supported := supportedAuthentication(provider, authentication)
		if !supported {
			continue
		}

		configured := configurations[providerID]
		operatorBaseURL := strings.TrimRight(strings.TrimSpace(configured.Connector.BaseURL), "/")
		baseURL := strings.TrimSpace(configured.Connector.BaseURL)
		if baseURL == "" {
			baseURL = strings.TrimSpace(provider.Inference.BaseURL)
		}
		if baseURL == "" {
			continue
		}
		configured.Connector.BaseURL = strings.TrimRight(baseURL, "/")
		if configured.Connector.Timeout <= 0 {
			configured.Connector.Timeout = 30 * time.Second
		}
		if configured.Connector.MaxConnections <= 0 {
			configured.Connector.MaxConnections = 100
		}
		configured.Connector.Enabled = true

		offerings, err := catalog.ProviderOfferings(providerID)
		if err != nil {
			return nil, fmt.Errorf("%s: read offerings: %w", providerID, err)
		}
		operations := make(map[catalogs.ProviderOperation]struct{})
		endpointTypes := make(map[catalogs.EndpointType]struct{})
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
					continue
				}
				operations[operation] = struct{}{}
				endpointTypes[endpoint.Type] = struct{}{}
			}
		}
		if len(endpointTypes) == 0 {
			continue
		}

		activation := Activation{
			ProviderID:      providerID,
			Configuration:   configured,
			RequiresAuth:    requiresAuth,
			Anonymous:       anonymous,
			OperatorBaseURL: operatorBaseURL,
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

func supportedAuthentication(
	provider catalogs.Provider,
	authentication *providerauth.Registry,
) (bool, credentials.Material, bool) {
	if provider.Credentials == nil {
		return true, credentials.Material{}, false
	}
	profiles := make(map[catalogs.ProviderCredentialProfileID]catalogs.ProviderCredentialProfile)
	for _, profile := range provider.Credentials.Profiles {
		profiles[profile.ID] = profile
	}
	fields := make(map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField)
	for _, field := range provider.Credentials.Fields {
		fields[field.ID] = field
	}
	requiresAuth := true
	var anonymous credentials.Material
	var supported bool
	for _, profileID := range provider.Credentials.Inference.Alternatives {
		profile, exists := profiles[profileID]
		if !exists || !authentication.Supports(profile.Primitive) {
			continue
		}
		supported = true
		if profile.Primitive != catalogs.ProviderAuthenticationNone {
			continue
		}
		requiresAuth = false
		if anonymous.Empty() {
			anonymous = defaultAnonymousMaterial(profile, fields)
		}
	}
	return requiresAuth, anonymous, supported
}

func defaultAnonymousMaterial(
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) credentials.Material {
	values := make(map[catalogs.ProviderCredentialFieldID]string)
	for _, fieldID := range profile.Fields {
		field, exists := fields[fieldID]
		if !exists || field.Kind != catalogs.ProviderCredentialFieldParameter {
			return credentials.Material{}
		}
		if field.Required && strings.TrimSpace(field.Default) == "" {
			return credentials.Material{}
		}
		if field.Default != "" {
			values[fieldID] = field.Default
		}
	}
	return credentials.NewMaterial(profile, values, credentials.MaterialMetadata{Version: "catalog-default"})
}
