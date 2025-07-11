package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// ModelsHandler handles model-related endpoints
type ModelsHandler struct {
	*BaseHandler
}

// NewModelsHandler creates a new models handler
func NewModelsHandler(service proxy.Service) *ModelsHandler {
	return &ModelsHandler{
		BaseHandler: NewBaseHandler(service),
	}
}

// List handles GET /v1/models and /api/v1/models
func (h *ModelsHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get models from service
	resp, err := h.service.ListModels(ctx)
	if err != nil {
		h.logError(ctx, err, "failed to list models")
		h.writeError(w, err)
		return
	}

	// Set cache headers
	h.setCacheHeaders(ctx, w)

	// For /v1/models, return basic OpenAI format
	// For /api/v1/models, return enhanced metadata (already included in response)
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		// Transform to basic OpenAI format by removing extra fields
		basicResp := transformToBasicModels(resp)
		if err := dto.WriteJSON(w, http.StatusOK, basicResp); err != nil {
			h.logError(ctx, err, "failed to write response")
		}
	} else {
		// Return full response with metadata
		if err := dto.WriteJSON(w, http.StatusOK, resp); err != nil {
			h.logError(ctx, err, "failed to write response")
		}
	}
}

// Get handles GET /v1/models/{model} and /api/v1/models/{model}
func (h *ModelsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get model ID from path
	modelID := chi.URLParam(r, "model")
	if modelID == "" {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Model ID is required")
		return
	}

	// URL decode the model ID (handles cases like "anthropic%2Fclaude-3-opus")
	modelID, err := url.QueryUnescape(modelID)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid model ID")
		return
	}

	// Get all models
	resp, err := h.service.ListModels(ctx)
	if err != nil {
		h.logError(ctx, err, "failed to list models")
		h.writeError(w, err)
		return
	}

	// Find the requested model
	for _, model := range resp.Data {
		if model.ID == modelID {
			// For /v1/models/{model}, return basic format
			// For /api/v1/models/{model}, return enhanced format
			if strings.HasPrefix(r.URL.Path, "/v1/") {
				basicModel := transformToBasicModel(&model)
				if err := dto.WriteJSON(w, http.StatusOK, basicModel); err != nil {
					h.logError(ctx, err, "failed to write response")
				}
			} else {
				if err := dto.WriteJSON(w, http.StatusOK, model); err != nil {
					h.logError(ctx, err, "failed to write response")
				}
			}
			return
		}
	}

	// Model not found
	dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Model not found")
}

// GetEndpoints handles GET /api/v1/models/{model}/endpoints
func (h *ModelsHandler) GetEndpoints(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get model ID from path
	modelID := chi.URLParam(r, "model")
	if modelID == "" {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Model ID is required")
		return
	}

	// URL decode the model ID
	modelID, err := url.QueryUnescape(modelID)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid model ID")
		return
	}

	// Get endpoints for model
	resp, err := h.service.GetModelEndpoints(ctx, modelID)
	if err != nil {
		h.logError(ctx, err, "failed to get model endpoints")
		h.writeError(w, err)
		return
	}

	// Write response
	if err := dto.WriteJSON(w, http.StatusOK, resp); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}

// transformToBasicModels removes enhanced metadata for OpenAI compatibility
func transformToBasicModels(resp *proxy.ModelsResponse) interface{} {
	type basicModel struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	type basicResponse struct {
		Object string       `json:"object"`
		Data   []basicModel `json:"data"`
	}

	basic := basicResponse{
		Object: resp.Object,
		Data:   make([]basicModel, len(resp.Data)),
	}

	for i, model := range resp.Data {
		basic.Data[i] = basicModel{
			ID:      model.ID,
			Object:  model.Object,
			Created: model.Created,
			OwnedBy: model.OwnedBy,
		}
	}

	return basic
}

// transformToBasicModel removes enhanced metadata for OpenAI compatibility
func transformToBasicModel(model *proxy.ModelInfo) interface{} {
	type basicModel struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	return basicModel{
		ID:      model.ID,
		Object:  model.Object,
		Created: model.Created,
		OwnedBy: model.OwnedBy,
	}
}
