package connectors

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

var (
	// ErrTransportUnsupported reports a catalog endpoint type without a
	// compiled inference transport.
	ErrTransportUnsupported = errors.New("provider inference transport is unsupported")
	// ErrTransportOperationUnsupported reports an operation that a compiled
	// transport does not implement.
	ErrTransportOperationUnsupported = errors.New("provider inference transport operation is unsupported")
)

// TransportFactory constructs one provider-scoped instance of a compiled wire
// transport. Provider membership does not belong in a factory.
type TransportFactory func(catalogs.ProviderID, ProviderConfig) (Connector, error)

// TransportDescriptor defines one compiled endpoint protocol and its
// operations.
type TransportDescriptor struct {
	EndpointType catalogs.EndpointType
	Operations   []catalogs.ProviderOperation
	Factory      TransportFactory
}

// TransportRegistry stores compiled inference behavior by endpoint protocol.
type TransportRegistry struct {
	descriptors map[catalogs.EndpointType]TransportDescriptor
}

// NewTransportRegistry validates and stores endpoint protocol descriptors.
func NewTransportRegistry(descriptors ...TransportDescriptor) (*TransportRegistry, error) {
	if len(descriptors) == 0 {
		return nil, errors.New("provider inference transports are required")
	}
	registry := &TransportRegistry{
		descriptors: make(map[catalogs.EndpointType]TransportDescriptor, len(descriptors)),
	}
	for _, descriptor := range descriptors {
		if strings.TrimSpace(string(descriptor.EndpointType)) == "" || descriptor.Factory == nil ||
			len(descriptor.Operations) == 0 {
			return nil, errors.New("provider inference transport descriptor is invalid")
		}
		if _, exists := registry.descriptors[descriptor.EndpointType]; exists {
			return nil, fmt.Errorf("%s: duplicate transport descriptor", descriptor.EndpointType)
		}
		seen := make(map[catalogs.ProviderOperation]struct{}, len(descriptor.Operations))
		for _, operation := range descriptor.Operations {
			if operation != catalogs.ProviderOperationChatCompletions &&
				operation != catalogs.ProviderOperationEmbeddings {
				return nil, fmt.Errorf("%s operation %q is invalid", descriptor.EndpointType, operation)
			}
			if _, exists := seen[operation]; exists {
				return nil, fmt.Errorf("%s operation %q is duplicated", descriptor.EndpointType, operation)
			}
			seen[operation] = struct{}{}
		}
		descriptor.Operations = append([]catalogs.ProviderOperation(nil), descriptor.Operations...)
		registry.descriptors[descriptor.EndpointType] = descriptor
	}
	return registry, nil
}

// ProductionTransportRegistry returns Starport's compiled wire protocols.
func ProductionTransportRegistry() (*TransportRegistry, error) {
	chat := catalogs.ProviderOperationChatCompletions
	embeddings := catalogs.ProviderOperationEmbeddings
	return NewTransportRegistry(
		TransportDescriptor{
			EndpointType: catalogs.EndpointTypeOpenAI,
			Operations:   []catalogs.ProviderOperation{chat, embeddings},
			Factory: func(providerID catalogs.ProviderID, config ProviderConfig) (Connector, error) {
				return newOpenAIConnector(providerID, string(providerID), config)
			},
		},
		TransportDescriptor{
			EndpointType: catalogs.EndpointTypeAnthropic,
			Operations:   []catalogs.ProviderOperation{chat},
			Factory: func(providerID catalogs.ProviderID, config ProviderConfig) (Connector, error) {
				return newAnthropicConnector(string(providerID), config)
			},
		},
		TransportDescriptor{
			EndpointType: catalogs.EndpointTypeGoogle,
			Operations:   []catalogs.ProviderOperation{chat, embeddings},
			Factory: func(providerID catalogs.ProviderID, config ProviderConfig) (Connector, error) {
				return newGoogleAIStudioConnector(string(providerID), config)
			},
		},
		TransportDescriptor{
			EndpointType: catalogs.EndpointTypeGoogleCloud,
			Operations:   []catalogs.ProviderOperation{chat, embeddings},
			Factory: func(providerID catalogs.ProviderID, config ProviderConfig) (Connector, error) {
				return newGoogleCloudConnector(string(providerID), config)
			},
		},
		TransportDescriptor{
			EndpointType: catalogs.EndpointTypeOllama,
			Operations:   []catalogs.ProviderOperation{chat, embeddings},
			Factory: func(providerID catalogs.ProviderID, config ProviderConfig) (Connector, error) {
				return newOllamaConnector(string(providerID), config)
			},
		},
	)
}

// Supports reports whether a compiled endpoint protocol implements an
// operation.
func (r *TransportRegistry) Supports(
	endpointType catalogs.EndpointType,
	operation catalogs.ProviderOperation,
) bool {
	if r == nil {
		return false
	}
	descriptor, exists := r.descriptors[endpointType]
	if !exists {
		return false
	}
	for _, supported := range descriptor.Operations {
		if supported == operation {
			return true
		}
	}
	return false
}

// EndpointTypes returns compiled protocols in stable order.
func (r *TransportRegistry) EndpointTypes() []catalogs.EndpointType {
	if r == nil {
		return nil
	}
	types := make([]catalogs.EndpointType, 0, len(r.descriptors))
	for endpointType := range r.descriptors {
		types = append(types, endpointType)
	}
	sort.Slice(types, func(left, right int) bool { return types[left] < types[right] })
	return types
}

// NewProviderConnector composes each catalog-selected protocol for one
// provider. The provider ID labels runtime state; it does not select behavior.
func (r *TransportRegistry) NewProviderConnector(
	providerID catalogs.ProviderID,
	endpointTypes []catalogs.EndpointType,
	config ProviderConfig,
) (Connector, error) {
	if r == nil {
		return nil, errors.New("provider inference transport registry is required")
	}
	if strings.TrimSpace(string(providerID)) == "" {
		return nil, errors.New("provider ID is required")
	}
	types := append([]catalogs.EndpointType(nil), endpointTypes...)
	sort.Slice(types, func(left, right int) bool { return types[left] < types[right] })
	transports := make(map[catalogs.EndpointType]Connector, len(types))
	for _, endpointType := range types {
		if _, exists := transports[endpointType]; exists {
			continue
		}
		descriptor, exists := r.descriptors[endpointType]
		if !exists {
			return nil, errors.Join(
				fmt.Errorf("%s: %w", endpointType, ErrTransportUnsupported),
				closeTransportConnectors(transports),
			)
		}
		connector, err := descriptor.Factory(providerID, config)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("create %s transport: %w", endpointType, err),
				closeTransportConnectors(transports),
			)
		}
		if connector == nil {
			return nil, errors.Join(
				fmt.Errorf("create %s transport: connector is nil", endpointType),
				closeTransportConnectors(transports),
			)
		}
		transports[endpointType] = connector
	}
	if len(transports) == 0 {
		return nil, ErrTransportUnsupported
	}
	return &providerConnector{providerID: providerID, transports: transports}, nil
}

type providerConnector struct {
	providerID catalogs.ProviderID
	transports map[catalogs.EndpointType]Connector
}

func (c *providerConnector) Chat(ctx context.Context, request *ChatRequest) (*ChatResponse, error) {
	connector, err := c.transport(request.Endpoint.Type)
	if err != nil {
		return nil, err
	}
	return connector.Chat(ctx, request)
}

func (c *providerConnector) ChatStream(ctx context.Context, request *ChatRequest) (ChatStream, error) {
	connector, err := c.transport(request.Endpoint.Type)
	if err != nil {
		return nil, err
	}
	return connector.ChatStream(ctx, request)
}

func (c *providerConnector) Embeddings(
	ctx context.Context,
	request *EmbeddingsRequest,
) (*EmbeddingsResponse, error) {
	connector, err := c.transport(request.Endpoint.Type)
	if err != nil {
		return nil, err
	}
	return connector.Embeddings(ctx, request)
}

func (c *providerConnector) Name() string { return string(c.providerID) }

func (c *providerConnector) Close() error { return closeTransportConnectors(c.transports) }

func (c *providerConnector) transport(endpointType catalogs.EndpointType) (Connector, error) {
	connector, exists := c.transports[endpointType]
	if !exists {
		return nil, fmt.Errorf("%s: %w", endpointType, ErrTransportUnsupported)
	}
	return connector, nil
}

func closeTransportConnectors(transports map[catalogs.EndpointType]Connector) error {
	var closeErrors []error
	for endpointType, connector := range transports {
		if connector == nil {
			continue
		}
		if err := connector.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close %s transport: %w", endpointType, err))
		}
	}
	return errors.Join(closeErrors...)
}
