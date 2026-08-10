package connectors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/providerauth"
)

var (
	// ErrAdapterRegistryRequired reports an absent adapter registry.
	ErrAdapterRegistryRequired = errors.New("adapter registry is required")
	// ErrAdapterDescriptorInvalid reports an incomplete adapter descriptor.
	ErrAdapterDescriptorInvalid = errors.New("adapter descriptor is invalid")
	// ErrAdapterProviderMissingCatalog reports configured adapter code without a Starmap provider contract.
	ErrAdapterProviderMissingCatalog = errors.New("configured adapter provider is missing from Starmap")
	// ErrAdapterProviderMissingOffering reports a configured adapter without a compatible Starmap offering.
	ErrAdapterProviderMissingOffering = errors.New("configured adapter provider has no compatible Starmap offering")
	// ErrAdapterProviderUnsupported reports a provider without compiled inference adapter code.
	ErrAdapterProviderUnsupported = errors.New("provider has no compiled inference adapter")
	// ErrAdapterConfigurationInvalid reports an incomplete inference adapter configuration.
	ErrAdapterConfigurationInvalid = errors.New("inference adapter configuration is invalid")
)

// ConnectorFactory constructs one provider inference adapter.
type ConnectorFactory func(ProviderConfig) (Connector, error)

// AdapterConfigured reports whether operator configuration requests one adapter.
type AdapterConfigured func(ProviderConfig) bool

// AdapterConfigValidator validates provider-specific operator configuration.
type AdapterConfigValidator func(ProviderConfig) error

// AdapterBaseURLResolver converts Starmap service facts and an operator override
// into the base URL expected by one compiled adapter.
type AdapterBaseURLResolver func(catalogs.Provider, ProviderConfig) (string, error)

// InferenceCredentialValidator validates local inference credential fields.
type InferenceCredentialValidator func(context.Context, map[string]string, map[string]any) error

// InferenceCredentialProbe performs an optional network verification. It is
// separate from local validation so callers must opt into external I/O.
type InferenceCredentialProbe func(context.Context, catalogs.Provider, map[string]string, map[string]any) error

// InferenceCredentialField defines one Starport-owned inference credential field.
type InferenceCredentialField struct {
	Name      string
	Required  bool
	Sensitive bool
}

// InferenceCredentialDescriptor owns one adapter's inference authentication contract.
type InferenceCredentialDescriptor struct {
	Fields      []InferenceCredentialField
	Header      string
	Scheme      string
	Validate    InferenceCredentialValidator
	Probe       InferenceCredentialProbe
	Description string
}

// AdapterDescriptor owns compiled behavior for one provider inference adapter.
// Provider and offering facts remain in Starmap.
type AdapterDescriptor struct {
	ProviderID     catalogs.ProviderID
	Operations     []catalogs.ProviderOperation
	EndpointTypes  []catalogs.EndpointType
	Factory        ConnectorFactory
	Configured     AdapterConfigured
	ValidateConfig AdapterConfigValidator
	ResolveBaseURL AdapterBaseURLResolver
	Credential     InferenceCredentialDescriptor
}

// AdapterActivation is one member of the catalog, adapter, and configuration intersection.
type AdapterActivation struct {
	ProviderID catalogs.ProviderID
	Descriptor AdapterDescriptor
	Config     ProviderConfig
}

// AdapterRegistry is the only compiled provider-adapter roster.
type AdapterRegistry struct {
	descriptors map[catalogs.ProviderID]AdapterDescriptor
}

var productionAdapters, productionAdaptersErr = NewAdapterRegistry(productionAdapterDescriptors()...)

// NewAdapterRegistry validates and stores adapter descriptors by exact Starmap provider ID.
func NewAdapterRegistry(descriptors ...AdapterDescriptor) (*AdapterRegistry, error) {
	registry := &AdapterRegistry{descriptors: make(map[catalogs.ProviderID]AdapterDescriptor, len(descriptors))}
	for _, descriptor := range descriptors {
		if err := validateAdapterDescriptor(descriptor); err != nil {
			return nil, err
		}
		if _, exists := registry.descriptors[descriptor.ProviderID]; exists {
			return nil, fmt.Errorf("%s: duplicate provider: %w", descriptor.ProviderID, ErrAdapterDescriptorInvalid)
		}
		descriptor.Operations = append([]catalogs.ProviderOperation(nil), descriptor.Operations...)
		descriptor.EndpointTypes = append([]catalogs.EndpointType(nil), descriptor.EndpointTypes...)
		descriptor.Credential.Fields = append(
			[]InferenceCredentialField(nil), descriptor.Credential.Fields...,
		)
		registry.descriptors[descriptor.ProviderID] = descriptor
	}
	return registry, nil
}

// ProductionAdapterRegistry returns Starport's compiled v1 inference adapters.
func ProductionAdapterRegistry() (*AdapterRegistry, error) {
	return productionAdapters, productionAdaptersErr
}

// Descriptor returns a copy of one adapter descriptor.
func (r *AdapterRegistry) Descriptor(providerID catalogs.ProviderID) (AdapterDescriptor, bool) {
	if r == nil {
		return AdapterDescriptor{}, false
	}
	descriptor, found := r.descriptors[providerID]
	if !found {
		return AdapterDescriptor{}, false
	}
	descriptor.Operations = append([]catalogs.ProviderOperation(nil), descriptor.Operations...)
	descriptor.EndpointTypes = append([]catalogs.EndpointType(nil), descriptor.EndpointTypes...)
	descriptor.Credential.Fields = append(
		[]InferenceCredentialField(nil), descriptor.Credential.Fields...,
	)
	return descriptor, true
}

// ProviderIDs returns compiled adapter IDs in stable order.
func (r *AdapterRegistry) ProviderIDs() []catalogs.ProviderID {
	if r == nil {
		return nil
	}
	ids := make([]catalogs.ProviderID, 0, len(r.descriptors))
	for providerID := range r.descriptors {
		ids = append(ids, providerID)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}

// Activate derives the complete active-provider intersection. Configured
// providers fail closed when either the catalog contract or adapter is absent.
func (r *AdapterRegistry) Activate(
	catalog *catalogs.Catalog,
	configurations map[catalogs.ProviderID]ProviderConfig,
) ([]AdapterActivation, error) {
	if r == nil {
		return nil, ErrAdapterRegistryRequired
	}
	if catalog == nil {
		return nil, fmt.Errorf("catalog is required")
	}
	providerIDs := make([]catalogs.ProviderID, 0, len(configurations))
	for providerID := range configurations {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Slice(providerIDs, func(left, right int) bool { return providerIDs[left] < providerIDs[right] })

	active := make([]AdapterActivation, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		config := configurations[providerID]
		descriptor, exists := r.descriptors[providerID]
		if !exists {
			if inferenceConfigurationPresent(config) {
				return nil, fmt.Errorf("%s: %w", providerID, ErrAdapterProviderUnsupported)
			}
			continue
		}
		configured := descriptor.Configured
		if configured == nil {
			configured = APIKeyConfigured
		}
		if !configured(config) {
			continue
		}
		provider, err := catalog.Provider(providerID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", providerID, ErrAdapterProviderMissingCatalog)
		}
		offerings, err := catalog.ProviderOfferings(providerID)
		if err != nil {
			return nil, fmt.Errorf("%s: read offerings: %w", providerID, err)
		}
		if descriptor.ValidateConfig != nil {
			if err := descriptor.ValidateConfig(config); err != nil {
				return nil, fmt.Errorf("%s: %w: %v", providerID, ErrAdapterConfigurationInvalid, err)
			}
		}
		if descriptor.ResolveBaseURL != nil {
			config.BaseURL, err = descriptor.ResolveBaseURL(provider, config)
			if err != nil {
				return nil, fmt.Errorf("%s: %w: %v", providerID, ErrAdapterConfigurationInvalid, err)
			}
		}
		compatible, bindingErr := hasCompatibleOffering(descriptor, provider, offerings, config)
		if bindingErr != nil {
			return nil, fmt.Errorf("%s: %w: %v", providerID, ErrAdapterConfigurationInvalid, bindingErr)
		}
		if !compatible {
			return nil, fmt.Errorf("%s: %w", providerID, ErrAdapterProviderMissingOffering)
		}
		active = append(active, AdapterActivation{
			ProviderID: providerID,
			Descriptor: descriptor,
			Config:     config,
		})
	}
	return active, nil
}

func hasCompatibleOffering(
	descriptor AdapterDescriptor,
	provider catalogs.Provider,
	offerings []catalogs.ProviderOffering,
	config ProviderConfig,
) (bool, error) {
	for _, offering := range offerings {
		if offering.Availability == catalogs.OfferingAvailabilityUnavailable ||
			offering.Lifecycle == catalogs.OfferingLifecycleRetired {
			continue
		}
		for _, operation := range descriptor.Operations {
			endpoint, found := offering.Endpoint(operation)
			if offering.Supports(operation) && found && descriptor.SupportsEndpoint(endpoint.Type) {
				if provider.Inference == nil {
					return false, errors.New("starmap inference service is required")
				}
				if _, err := provider.Inference.BindOfferingEndpoint(
					endpoint,
					config.BaseURL,
					config.EndpointBindings,
				); err != nil {
					return false, err
				}
				return true, nil
			}
		}
	}
	return false, nil
}

// NewConnector constructs one connector through the registered descriptor.
func (r *AdapterRegistry) NewConnector(providerID catalogs.ProviderID, config ProviderConfig) (Connector, error) {
	descriptor, found := r.Descriptor(providerID)
	if !found {
		return nil, fmt.Errorf("%s: %w", providerID, ErrAdapterProviderUnsupported)
	}
	configured := descriptor.Configured
	if configured == nil {
		configured = APIKeyConfigured
	}
	if !configured(config) {
		return nil, fmt.Errorf("%s: adapter is not configured: %w", providerID, ErrAdapterConfigurationInvalid)
	}
	if descriptor.ValidateConfig != nil {
		if err := descriptor.ValidateConfig(config); err != nil {
			return nil, fmt.Errorf("%s: %w: %v", providerID, ErrAdapterConfigurationInvalid, err)
		}
	}
	return descriptor.Factory(config)
}

// ValidateCredential validates Starport inference credentials without network I/O.
func (r *AdapterRegistry) ValidateCredential(
	ctx context.Context,
	providerID catalogs.ProviderID,
	key map[string]string,
	config map[string]any,
) error {
	descriptor, found := r.Descriptor(providerID)
	if !found {
		return fmt.Errorf("%s: %w", providerID, ErrAdapterProviderUnsupported)
	}
	if descriptor.Credential.Validate == nil {
		if len(descriptor.Credential.Fields) == 0 {
			return nil
		}
		return fmt.Errorf("%s: inference credential validator is missing: %w", providerID, ErrAdapterDescriptorInvalid)
	}
	return descriptor.Credential.Validate(ctx, key, config)
}

// ProbeCredential performs an explicit external inference-credential probe.
func (r *AdapterRegistry) ProbeCredential(
	ctx context.Context,
	provider catalogs.Provider,
	key map[string]string,
	config map[string]any,
) error {
	descriptor, found := r.Descriptor(provider.ID)
	if !found {
		return fmt.Errorf("%s: %w", provider.ID, ErrAdapterProviderUnsupported)
	}
	if descriptor.Credential.Probe == nil {
		return nil
	}
	return descriptor.Credential.Probe(ctx, provider, key, config)
}

// ApplyInferenceAuth applies the registered inference credential placement.
func (r *AdapterRegistry) ApplyInferenceAuth(
	providerID catalogs.ProviderID,
	request *http.Request,
	secret string,
) error {
	if request == nil {
		return fmt.Errorf("request is required")
	}
	descriptor, found := r.Descriptor(providerID)
	if !found {
		return fmt.Errorf("%s: %w", providerID, ErrAdapterProviderUnsupported)
	}
	credential := descriptor.Credential
	if credential.Header == "" {
		if secret == "" {
			return nil
		}
		return fmt.Errorf("%s: inference authentication placement is missing: %w", providerID, ErrAdapterDescriptorInvalid)
	}
	value := secret
	if credential.Scheme != "" {
		value = credential.Scheme + " " + secret
	}
	request.Header.Set(credential.Header, value)
	return nil
}

func applyRegisteredInferenceAuth(providerID catalogs.ProviderID, request *http.Request, secret string) {
	if productionAdaptersErr != nil || productionAdapters == nil || request == nil {
		return
	}
	_ = productionAdapters.ApplyInferenceAuth(providerID, request, secret)
}

// Supports reports whether compiled adapter code implements an operation.
func (d AdapterDescriptor) Supports(operation catalogs.ProviderOperation) bool {
	for _, supported := range d.Operations {
		if supported == operation {
			return true
		}
	}
	return false
}

// SupportsEndpoint reports whether compiled adapter code implements a wire protocol.
func (d AdapterDescriptor) SupportsEndpoint(endpointType catalogs.EndpointType) bool {
	for _, supported := range d.EndpointTypes {
		if supported == endpointType {
			return true
		}
	}
	return false
}

// APIKeyConfigured reports whether operator configuration requests an API-key adapter.
func APIKeyConfigured(config ProviderConfig) bool {
	return config.Enabled || strings.TrimSpace(config.APIKey) != ""
}

func validateAdapterDescriptor(descriptor AdapterDescriptor) error {
	if strings.TrimSpace(string(descriptor.ProviderID)) == "" || descriptor.Factory == nil {
		return ErrAdapterDescriptorInvalid
	}
	seen := make(map[catalogs.ProviderOperation]struct{}, len(descriptor.Operations))
	for _, operation := range descriptor.Operations {
		switch operation {
		case catalogs.ProviderOperationChatCompletions, catalogs.ProviderOperationEmbeddings:
		default:
			return fmt.Errorf("%s operation %q: %w", descriptor.ProviderID, operation, ErrAdapterDescriptorInvalid)
		}
		if _, exists := seen[operation]; exists {
			return fmt.Errorf("%s duplicate operation %q: %w", descriptor.ProviderID, operation, ErrAdapterDescriptorInvalid)
		}
		seen[operation] = struct{}{}
	}
	if len(descriptor.EndpointTypes) == 0 {
		return fmt.Errorf("%s endpoint types: %w", descriptor.ProviderID, ErrAdapterDescriptorInvalid)
	}
	endpointTypes := make(map[catalogs.EndpointType]struct{}, len(descriptor.EndpointTypes))
	for _, endpointType := range descriptor.EndpointTypes {
		switch endpointType {
		case catalogs.EndpointTypeOpenAI,
			catalogs.EndpointTypeAnthropic,
			catalogs.EndpointTypeGoogle,
			catalogs.EndpointTypeGoogleCloud,
			catalogs.EndpointTypeOllama:
		default:
			return fmt.Errorf("%s endpoint type %q: %w", descriptor.ProviderID, endpointType, ErrAdapterDescriptorInvalid)
		}
		if _, exists := endpointTypes[endpointType]; exists {
			return fmt.Errorf("%s duplicate endpoint type %q: %w", descriptor.ProviderID, endpointType, ErrAdapterDescriptorInvalid)
		}
		endpointTypes[endpointType] = struct{}{}
	}
	fieldNames := make(map[string]struct{}, len(descriptor.Credential.Fields))
	for _, field := range descriptor.Credential.Fields {
		if strings.TrimSpace(field.Name) == "" {
			return fmt.Errorf("%s credential field: %w", descriptor.ProviderID, ErrAdapterDescriptorInvalid)
		}
		if _, exists := fieldNames[field.Name]; exists {
			return fmt.Errorf("%s duplicate credential field %q: %w", descriptor.ProviderID, field.Name, ErrAdapterDescriptorInvalid)
		}
		fieldNames[field.Name] = struct{}{}
	}
	return nil
}

func inferenceConfigurationPresent(config ProviderConfig) bool {
	return config.Enabled || strings.TrimSpace(config.APIKey) != "" ||
		config.AuthMode != "" || strings.TrimSpace(config.BaseURL) != "" ||
		len(config.EndpointBindings) > 0
}

func requiredAPIKeyConfig(config ProviderConfig) error {
	if strings.TrimSpace(config.APIKey) == "" {
		return errors.New("inference API key is required")
	}
	return nil
}

func requiredCloudCredentialConfig(config ProviderConfig) error {
	if err := config.AuthMode.Validate(); err != nil {
		return err
	}
	if config.CredentialSource != nil && config.AuthMode != providerauth.ModeDefault {
		return errors.New("injected credential source requires default auth mode")
	}
	switch config.AuthMode {
	case providerauth.ModeDefault:
		if strings.TrimSpace(config.APIKey) != "" {
			return errors.New("inference API key cannot be combined with default credentials")
		}
		return nil
	case providerauth.ModeStatic:
		return requiredAPIKeyConfig(config)
	case "":
		return errors.New("provider auth mode is required")
	default:
		return errors.New("unsupported provider auth mode")
	}
}

func requiredAzureConfig(config ProviderConfig) error {
	if err := requiredCloudCredentialConfig(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		return errors.New("resource base URL is required")
	}
	return validateHTTPURL(config.BaseURL)
}

func azureConfigured(config ProviderConfig) bool {
	return config.Enabled || strings.TrimSpace(config.APIKey) != "" || config.AuthMode != "" ||
		strings.TrimSpace(config.BaseURL) != ""
}

func ollamaConfigured(config ProviderConfig) bool { return config.Enabled }

func resolveProviderBase(provider catalogs.Provider, config ProviderConfig) (string, error) {
	if strings.TrimSpace(config.BaseURL) != "" {
		return strings.TrimRight(config.BaseURL, "/"), nil
	}
	if provider.Inference == nil || strings.TrimSpace(provider.Inference.BaseURL) == "" {
		return "", errors.New("starmap inference base URL is required")
	}
	return strings.TrimRight(provider.Inference.BaseURL, "/"), nil
}

func validateHTTPURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("invalid endpoint URL")
	}
	return nil
}
