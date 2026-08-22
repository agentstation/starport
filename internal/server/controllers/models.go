package controllers

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/dto"
)

// ModelsController handles model-related endpoints
type ModelsController struct {
	*BaseHandler
}

// NewModelsController creates a new models controller
func NewModelsController(service proxy.Proxy) *ModelsController {
	return newModelsController(service, ProtocolOpenAI)
}

// NewOpenRouterModelsController creates an OpenRouter models controller.
func NewOpenRouterModelsController(service proxy.Proxy) *ModelsController {
	return newModelsController(service, ProtocolOpenRouter)
}

func newModelsController(service proxy.Proxy, protocol Protocol) *ModelsController {
	return &ModelsController{
		BaseHandler: NewProtocolBaseHandler(service, protocol),
	}
}

// List handles GET /v1/models and /api/v1/models
func (h *ModelsController) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get models from service
	resp, err := h.service.ListModels(ctx)
	if err != nil {
		h.logError(ctx, err, "failed to list models")
		h.writeError(w, err)
		return
	}

	// Set cache headers from response
	if resp.CacheStatus != "" {
		w.Header().Set("X-Cache", resp.CacheStatus)
	}

	if err := h.writeModels(w, resp.Data); err != nil {
		h.logError(ctx, err, "failed to write response")
	}
}

// Get handles GET /v1/models/{model} and /api/v1/models/{model}
func (h *ModelsController) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get model ID from path
	modelID := chi.URLParam(r, "model")
	if modelID == "" {
		h.writeInvalidRequest(w, "Model ID is required")
		return
	}

	// URL decode provider-scoped model IDs that contain an escaped slash.
	modelID, err := url.QueryUnescape(modelID)
	if err != nil {
		h.writeInvalidRequest(w, "Invalid model ID")
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
			if err := h.writeModel(w, model); err != nil {
				h.logError(ctx, err, "failed to write response")
			}
			return
		}
	}

	// Model not found
	h.writeError(w, &proxy.ProviderError{Code: errorCodeNotFound, Message: "Model not found"})
}

// GetEndpoints handles GET /api/v1/models/{model}/endpoints
func (h *ModelsController) GetEndpoints(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get model ID from path
	modelID := chi.URLParam(r, "model")
	if modelID == "" {
		h.writeInvalidRequest(w, "Model ID is required")
		return
	}

	// URL decode the model ID
	modelID, err := url.QueryUnescape(modelID)
	if err != nil {
		h.writeInvalidRequest(w, "Invalid model ID")
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

func (h *ModelsController) writeModels(w http.ResponseWriter, models []proxy.ModelInfo) error {
	if h.protocol == ProtocolOpenRouter {
		converted := make([]openrouter.Model, len(models))
		for index := range models {
			converted[index] = openRouterModel(models[index])
		}
		return openrouter.WriteJSON(w, http.StatusOK, openrouter.ModelList{
			Data: converted, TotalCount: len(converted), Links: openrouter.ModelLinks{Next: nil},
		})
	}
	converted := make([]openai.Model, len(models))
	for index, model := range models {
		converted[index] = openAIModel(model)
	}
	return openai.WriteJSON(w, http.StatusOK, openai.ModelList{Object: "list", Data: converted})
}

func (h *ModelsController) writeModel(w http.ResponseWriter, model proxy.ModelInfo) error {
	if h.protocol == ProtocolOpenRouter {
		return openrouter.WriteJSON(w, http.StatusOK, openRouterModel(model))
	}
	return openai.WriteJSON(w, http.StatusOK, openAIModel(model))
}

func openAIModel(model proxy.ModelInfo) openai.Model {
	return openai.Model{
		ID:      model.ID,
		Object:  model.Object,
		Created: model.Created,
		OwnedBy: model.OwnedBy,
	}
}

func openRouterModel(model proxy.ModelInfo) openrouter.Model {
	converted := openrouter.Model{
		ID: model.ID, CanonicalSlug: model.CanonicalSlug, Name: model.Name,
		Created: model.Created, Description: model.Description,
		SupportedParameters: append([]string(nil), model.SupportedParameters...),
	}
	if converted.Name == "" {
		converted.Name = model.ID
	}
	if model.Context != nil {
		converted.ContextLength = *model.Context
	}
	if model.Pricing != nil {
		converted.Pricing = &openrouter.Pricing{Prompt: model.Pricing.Prompt, Completion: model.Pricing.Completion}
	}
	if model.Architecture != nil {
		converted.Architecture = &openrouter.Architecture{
			InputModalities:  append([]string(nil), model.Architecture.InputModalities...),
			OutputModalities: append([]string(nil), model.Architecture.OutputModalities...),
			Tokenizer:        model.Architecture.Tokenizer, InstructType: model.Architecture.InstructType,
		}
	}
	if model.TopProvider != nil {
		converted.TopProvider = &openrouter.TopProvider{
			ContextLength:       model.TopProvider.ContextLength,
			MaxCompletionTokens: model.TopProvider.MaxCompletionTokens,
		}
	}
	for _, author := range model.Authors {
		converted.Authors = append(converted.Authors, openrouter.ModelAuthor{ID: author.ID, Name: author.Name})
	}
	converted.Tags = append([]string(nil), model.Tags...)
	if model.Lineage != nil {
		converted.Lineage = &openrouter.ModelLineage{
			Family: model.Lineage.Family, Root: model.Lineage.Root, Parent: model.Lineage.Parent,
		}
	}
	converted.KnowledgeCutoff = model.KnowledgeCutoff
	converted.OpenWeights = model.OpenWeights
	for _, offering := range model.Offerings {
		converted.Offerings = append(converted.Offerings, openRouterOffering(offering))
	}
	return converted
}

func openRouterOffering(offering proxy.ModelOfferingInfo) openrouter.ModelOffering {
	converted := openrouter.ModelOffering{
		Provider:            offering.Provider,
		ProviderName:        offering.ProviderName,
		ProviderModelID:     offering.ProviderModelID,
		ContextLength:       offering.ContextLength,
		MaxCompletionTokens: offering.MaxCompletionTokens,
		Availability:        offering.Availability,
		Lifecycle:           offering.Lifecycle,
	}
	if offering.Pricing != nil {
		converted.Pricing = &openrouter.OfferingPricing{
			Prompt:     offering.Pricing.Prompt,
			Completion: offering.Pricing.Completion,
			Reasoning:  offering.Pricing.Reasoning,
			CacheRead:  offering.Pricing.CacheRead,
			CacheWrite: offering.Pricing.CacheWrite,
			Currency:   offering.Pricing.Currency,
		}
	}
	return converted
}
