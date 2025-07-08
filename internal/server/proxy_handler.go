package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// ProxyHandler handles LLM proxy requests
type ProxyHandler struct {
	connectorRegistry *ConnectorRegistry
	router            routing.ModelRouter
}

// ConnectorRegistry manages available connectors
type ConnectorRegistry struct {
	connectors map[string]connectors.Connector
}

// NewProxyHandler creates a new proxy handler
func NewProxyHandler(registry *ConnectorRegistry) *ProxyHandler {
	return &ProxyHandler{
		connectorRegistry: registry,
		router:            routing.NewRouter(registry),
	}
}

// NewConnectorRegistry creates a new connector registry
func NewConnectorRegistry() *ConnectorRegistry {
	return &ConnectorRegistry{
		connectors: make(map[string]connectors.Connector),
	}
}

// Register adds a connector to the registry
func (r *ConnectorRegistry) Register(provider string, connector connectors.Connector) {
	r.connectors[provider] = connector
}

// GetWithError retrieves a connector by provider name with error
func (r *ConnectorRegistry) GetWithError(provider string) (connectors.Connector, error) {
	connector, ok := r.connectors[provider]
	if !ok {
		return nil, fmt.Errorf("provider %s not found", provider)
	}
	return connector, nil
}

// GetByModel retrieves a connector based on model ID (provider/model format)
func (r *ConnectorRegistry) GetByModel(modelID string) (connectors.Connector, string, error) {
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) != 2 {
		return nil, "", fmt.Errorf("invalid model ID format: %s", modelID)
	}
	provider := parts[0]
	model := parts[1]
	
	connector, err := r.GetWithError(provider)
	if err != nil {
		return nil, "", err
	}
	
	return connector, model, nil
}

// RegisterRoutes adds proxy routes to the router
func (h *ProxyHandler) RegisterRoutes(r chi.Router) {
	// OpenAI-compatible endpoints
	r.Post("/v1/chat/completions", h.handleChatCompletions)
	r.Post("/v1/embeddings", h.handleEmbeddings)
	r.Get("/v1/models", h.handleModels)
}

// RegisterOpenRouterRoutes adds OpenRouter-compatible routes under /api/v1
func (h *ProxyHandler) RegisterOpenRouterRoutes(r chi.Router) {
	r.Post("/chat/completions", h.handleChatCompletions)
	r.Post("/embeddings", h.handleEmbeddings)
	r.Get("/models", h.handleModels)
	r.Get("/models/{model}/endpoints", h.handleModelEndpoints)
	r.Get("/providers", h.handleProviders)
}

// handleChatCompletions handles chat completion requests
func (h *ProxyHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Use the routing-aware handler
	h.handleChatCompletionsWithRouting(w, r)
}


// handleEmbeddings handles embeddings requests
func (h *ProxyHandler) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	// Parse request
	var req connectors.EmbeddingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body: "+err.Error())
		return
	}

	// Validate request
	if err := h.validateEmbeddingsRequest(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Get connector based on model
	connector, model, err := h.connectorRegistry.GetByModel(req.Model)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "model_not_found", "Model not found: "+req.Model)
		return
	}

	// Update model to remove provider prefix
	req.Model = model

	// Call connector
	ctx := r.Context()
	resp, err := connector.Embeddings(ctx, &req)
	if err != nil {
		h.handleConnectorError(w, err)
		return
	}

	// Transform response to include full model ID
	resp.Model = fmt.Sprintf("%s/%s", connector.Name(), resp.Model)

	// Write response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error().Err(err).Msg("failed to encode response")
	}
}

// handleModels handles model listing - returns basic format for /v1/models, enhanced for /api/v1/models
func (h *ProxyHandler) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	// Get basic models from connectors first
	basicModels := h.getAllModels(ctx)
	
	// Check if this is the OpenRouter-style endpoint
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		// Enhance with metadata for OpenRouter compatibility
		enhancedModels := []connectors.ModelMetadata{}
		for _, basicModel := range basicModels {
			metadata := connectors.GetModelMetadata(basicModel.ID)
			if metadata != nil {
				enhancedModels = append(enhancedModels, *metadata)
			}
		}
		
		// Create enhanced response
		resp := connectors.EnhancedModelsResponse{
			Object: "list",
			Data:   enhancedModels,
		}
		
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Error().Err(err).Msg("failed to encode response")
		}
	} else {
		// Return basic OpenAI-compatible response for /v1/models
		resp := connectors.ModelsResponse{
			Object: "list",
			Data:   basicModels,
		}
		
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Error().Err(err).Msg("failed to encode response")
		}
	}
}

// handleProviders handles provider listing (OpenRouter-compatible)
func (h *ProxyHandler) handleProviders(w http.ResponseWriter, _ *http.Request) {
	providers := connectors.GetProviderMetadata()
	
	resp := connectors.ProvidersResponse{
		Data: providers,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error().Err(err).Msg("failed to encode response")
	}
}

// handleModelEndpoints handles listing providers that offer a specific model
func (h *ProxyHandler) handleModelEndpoints(w http.ResponseWriter, r *http.Request) {
	modelParam := chi.URLParam(r, "model")
	if modelParam == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Model parameter is required")
		return
	}
	
	// URL decode the model parameter
	model := strings.ReplaceAll(modelParam, "%2F", "/")
	
	// Get providers for this model
	providers := connectors.GetProvidersForModel(model)
	
	resp := connectors.ModelProvidersResponse{
		Model:     model,
		Providers: providers,
	}
	
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error().Err(err).Msg("failed to encode response")
	}
}

// getAllModels aggregates models from all connectors
func (h *ProxyHandler) getAllModels(ctx context.Context) []connectors.Model {
	allModels := []connectors.Model{}
	
	for provider, connector := range h.connectorRegistry.connectors {
		resp, err := connector.Models(ctx)
		if err != nil {
			log.Warn().Err(err).Str("provider", provider).Msg("failed to get models")
			continue
		}
		
		// Transform model IDs to include provider prefix
		for _, model := range resp.Data {
			// Add provider prefix if not already present
			if !strings.Contains(model.ID, "/") {
				model.ID = fmt.Sprintf("%s/%s", provider, model.ID)
			}
			allModels = append(allModels, model)
		}
	}
	
	return allModels
}

// validateChatRequest validates a chat request
func (h *ProxyHandler) validateChatRequest(req *connectors.ChatRequest) error {
	// Either model or models array is required
	if req.Model == "" && len(req.Models) == 0 {
		return errors.New("model or models array is required")
	}
	if len(req.Messages) == 0 {
		return errors.New("messages are required")
	}
	
	// Validate temperature
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		return errors.New("temperature must be between 0 and 2")
	}
	
	// Validate top_p
	if req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		return errors.New("top_p must be between 0 and 1")
	}
	
	// Validate max_tokens
	if req.MaxTokens != nil && *req.MaxTokens < 1 {
		return errors.New("max_tokens must be at least 1")
	}
	
	return nil
}

// validateEmbeddingsRequest validates an embeddings request
func (h *ProxyHandler) validateEmbeddingsRequest(req *connectors.EmbeddingsRequest) error {
	if req.Model == "" {
		return errors.New("model is required")
	}
	if req.Input == nil {
		return errors.New("input is required")
	}
	
	// Validate encoding format
	if req.EncodingFormat != "" && req.EncodingFormat != "float" && req.EncodingFormat != "base64" {
		return errors.New("encoding_format must be 'float' or 'base64'")
	}
	
	return nil
}

// handleConnectorError handles errors from connectors
func (h *ProxyHandler) handleConnectorError(w http.ResponseWriter, err error) {
	var apiErr *connectors.APIError
	if errors.As(err, &apiErr) {
		h.writeError(w, apiErr.StatusCode, apiErr.Type, apiErr.Message)
		return
	}

	// Default error
	h.writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
}

// writeError writes an error response
func (h *ProxyHandler) writeError(w http.ResponseWriter, statusCode int, errorType, message string) {
	resp := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error().Err(err).Msg("failed to encode error response")
	}
}