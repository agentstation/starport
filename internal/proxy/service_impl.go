package proxy

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/routing"
)

// ServiceImpl implements the proxy Service interface
type ServiceImpl struct {
	registry *registry.Registry
	router   routing.ModelRouter
}

// NewService creates a new proxy service
func NewService(registry *registry.Registry, router routing.ModelRouter) Service {
	return &ServiceImpl{
		registry: registry,
		router:   router,
	}
}

// ProcessChatCompletion handles chat completion requests with routing
func (s *ServiceImpl) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Validate request
	if err := ValidateChatCompletionRequest(req); err != nil {
		return nil, err
	}

	// Transform to connector request
	connReq := TransformChatRequest(req)

	// Create routing request
	routingReq := &routing.Request{
		ChatRequest: connReq,
		Models:      req.Models,
		// TODO: Add provider preferences and API key config from context
	}

	// Route the request with fallback
	result, err := s.router.RouteWithFallback(ctx, routingReq)
	if err != nil {
		return nil, &RoutingError{
			Model:  req.Model,
			Reason: "failed to route request",
			Err:    err,
		}
	}

	// Transform response
	proxyResp := TransformChatResponse(result.ChatResponse, result.ModelUsed)

	// Add model_used field for OpenRouter compatibility
	proxyResp.ModelUsed = result.ModelUsed

	return proxyResp, nil
}

// ProcessChatCompletionStream handles streaming chat completion requests
func (s *ServiceImpl) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	// Validate request
	if err := ValidateChatCompletionRequest(req); err != nil {
		return nil, err
	}

	// Ensure streaming is requested
	if !req.Stream {
		return nil, ErrStreamingNotSupported
	}

	// Transform to connector request
	connReq := TransformChatRequest(req)
	connReq.Stream = true

	// Create routing request
	routingReq := &routing.Request{
		ChatRequest: connReq,
		Models:      req.Models,
		// TODO: Add provider preferences and API key config from context
	}

	// Select model using router
	modelID, connector, err := s.router.SelectModel(ctx, routingReq)
	if err != nil {
		return nil, &RoutingError{
			Model:  req.Model,
			Reason: "failed to route request",
			Err:    err,
		}
	}

	// Extract provider from model ID
	provider := extractProviderFromModelID(modelID)

	// Update request with selected model
	connReq.Model = modelID

	// Start streaming
	stream, err := connector.ChatStream(ctx, connReq)
	if err != nil {
		return nil, &ProviderError{
			Provider: provider,
			Code:     "stream_failed",
			Message:  "failed to start stream",
			Err:      err,
		}
	}

	// Wrap the stream to add model_used field
	wrappedStream := &streamWrapper{
		stream:  stream,
		modelID: modelID,
	}

	return wrappedStream, nil
}

// ProcessEmbeddings handles embedding generation requests
func (s *ServiceImpl) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Validate request
	if err := ValidateEmbeddingsRequest(req); err != nil {
		return nil, err
	}

	// Transform to connector request
	connReq := TransformEmbeddingsRequest(req)

	// Extract provider from model if specified
	provider, model := ExtractProviderFromModel(req.Model)

	var connector connectors.Connector
	var err error

	if provider != "" {
		// Direct provider specified
		connector, err = s.registry.Get(provider)
		if err != nil {
			return nil, &ProviderError{
				Provider: provider,
				Code:     "provider_not_found",
				Message:  "provider not available",
				Err:      err,
			}
		}
		connReq.Model = model
	} else {
		// Need to find a provider that supports this model
		// For embeddings, we'll use a simplified approach
		connector, provider, err = s.findEmbeddingsProvider(ctx, req.Model)
		if err != nil {
			return nil, err
		}
	}

	// Execute the request
	resp, err := connector.Embeddings(ctx, connReq)
	if err != nil {
		return nil, &ProviderError{
			Provider: provider,
			Code:     "embeddings_failed",
			Message:  "failed to generate embeddings",
			Err:      err,
		}
	}

	// Transform response
	proxyResp := TransformEmbeddingsResponse(resp)

	return proxyResp, nil
}

// ListModels returns available models based on routing configuration
func (s *ServiceImpl) ListModels(ctx context.Context) (*ModelsResponse, error) {
	models, err := s.registry.GetModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get models: %w", err)
	}

	// Transform []connectors.Model to response
	modelInfos := make([]ModelInfo, len(models))
	for i, m := range models {
		modelInfo := ModelInfo{
			ID:      m.ID,
			Object:  "model",
			Created: m.Created,
			OwnedBy: m.OwnedBy,
		}
		
		// Enrich with metadata if available
		metadata := connectors.GetModelMetadata(m.ID)
		if metadata != nil {
			if metadata.Pricing != nil {
				modelInfo.Pricing = &ModelPricing{
					Prompt:     metadata.Pricing.Prompt,
					Completion: metadata.Pricing.Completion,
					Currency:   "USD",
				}
			}
			if metadata.Context != nil {
				// Convert ModelContext (int) to *int
				ctx := int(*metadata.Context)
				modelInfo.Context = &ctx
			}
			if metadata.Description != "" {
				modelInfo.Description = metadata.Description
			}
			// Note: Architecture field not available in ModelInfo struct
			// Would need to extend ModelInfo to include architecture data
		}
		
		modelInfos[i] = modelInfo
	}

	return &ModelsResponse{
		Object: "list",
		Data:   modelInfos,
	}, nil
}

// ListProviders returns available provider information
func (s *ServiceImpl) ListProviders(_ context.Context) (*ProvidersResponse, error) {
	metadata := s.registry.GetProviderMetadata()

	// Transform to response
	providerInfos := make([]ProviderInfo, len(metadata))
	for i, m := range metadata {
		providerInfos[i] = ProviderInfo{
			ID:           m.Slug,
			Name:         m.Name,
			Description:  fmt.Sprintf("Provider: %s", m.Name),
			URL:          m.PrivacyPolicyURL,
			RequiresAuth: true,
		}
	}

	return &ProvidersResponse{
		Providers: providerInfos,
	}, nil
}

// GetModelEndpoints returns provider endpoints for a specific model
func (s *ServiceImpl) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	// Extract provider from model ID if present
	provider, model := ExtractProviderFromModel(modelID)

	endpoints := []EndpointInfo{}

	if provider != "" {
		// Check if this specific provider supports the model
		if s.registry.HasProvider(provider) {
			connector, _ := s.registry.Get(provider)
			modelsResp, _ := connector.Models(ctx)

			if modelsResp != nil && modelsResp.Data != nil {
				for _, m := range modelsResp.Data {
					if m.ID == modelID || m.ID == model {
						endpoints = append(endpoints, EndpointInfo{
							Provider:  provider,
							Endpoint:  "/api/v1/chat/completions",
							Available: true,
						})
						break
					}
				}
			}
		}
	} else {
		// Check all providers for this model
		for _, p := range s.registry.ListProviders() {
			connector, _ := s.registry.Get(p)
			modelsResp, _ := connector.Models(ctx)

			if modelsResp != nil && modelsResp.Data != nil {
				for _, m := range modelsResp.Data {
					// Check if model ID matches (with or without provider prefix)
					if m.ID == modelID || m.ID == fmt.Sprintf("%s/%s", p, modelID) {
						endpoints = append(endpoints, EndpointInfo{
							Provider:  p,
							Endpoint:  "/api/v1/chat/completions",
							Available: true,
						})
						break
					}
				}
			}
		}
	}

	return &ModelEndpointsResponse{
		Model:     modelID,
		Endpoints: endpoints,
	}, nil
}

// findEmbeddingsProvider finds a provider that supports embeddings for the given model
func (s *ServiceImpl) findEmbeddingsProvider(ctx context.Context, modelID string) (connectors.Connector, string, error) {
	// Check each provider for embeddings support
	for _, provider := range s.registry.ListProviders() {
		connector, _ := s.registry.Get(provider)

		// Check if provider has the model
		modelsResp, err := connector.Models(ctx)
		if err != nil || modelsResp == nil || modelsResp.Data == nil {
			continue
		}

		for _, model := range modelsResp.Data {
			if model.ID == modelID || model.ID == fmt.Sprintf("%s/%s", provider, modelID) {
				// Found the model, assume it supports embeddings if we got here
				// In a real implementation, we'd check model metadata
				return connector, provider, nil
			}
		}
	}

	return nil, "", ErrEmbeddingsNotSupported
}

// streamWrapper wraps a connector stream to add model_used field
type streamWrapper struct {
	stream  connectors.ChatStream
	modelID string
}

func (w *streamWrapper) Read() (*connectors.ChatStreamChunk, error) {
	chunk, err := w.stream.Recv()

	// Add model_used to the chunk if present
	if chunk != nil && chunk.Model == "" {
		chunk.Model = w.modelID
	}

	return chunk, err
}

func (w *streamWrapper) Close() error {
	return w.stream.Close()
}

// extractProviderFromModelID extracts the provider from a model ID
func extractProviderFromModelID(modelID string) string {
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
