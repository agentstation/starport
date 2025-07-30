package catalog

import (
	"strings"

	"github.com/agentstation/starport/pkg/models"
)

// ToModel converts a catalog Model to the internal models.Model type
func (m *Model) ToModel() *models.Model {
	model := &models.Model{
		ID:            m.ID,
		Created:       m.Created,
		Name:          m.Name,
		CanonicalSlug: m.CanonicalSlug,
		Description:   m.Description,
		ContextLength: int64(m.ContextLength),
		HuggingFaceID: m.HuggingFaceID,
	}

	// Extract provider from ID if in provider/model format
	if idx := strings.Index(m.ID, "/"); idx > 0 {
		model.Provider = m.ID[:idx]
	}

	// Convert Architecture
	if m.Architecture != nil {
		model.Architecture = &models.Architecture{
			InputModalities:  m.Architecture.InputModalities,
			OutputModalities: m.Architecture.OutputModalities,
			Tokenizer:        m.Architecture.Tokenizer,
			InstructType:     m.Architecture.InstructType,
		}
	}

	// Convert Pricing
	if m.Pricing != nil {
		model.Pricing = &models.Pricing{
			Prompt:            m.Pricing.Prompt,
			Completion:        m.Pricing.Completion,
			Request:           m.Pricing.Request,
			Image:             m.Pricing.Image,
			WebSearch:         m.Pricing.WebSearch,
			InternalReasoning: m.Pricing.InternalReasoning,
		}
	}

	// Convert TopProvider
	if m.TopProvider != nil {
		model.TopProvider = &models.TopProvider{
			MaxCompletionTokens: int64(m.TopProvider.MaxCompletionTokens),
			ContextLength:       int64(m.TopProvider.ContextLength),
		}
	}

	// Copy supported parameters
	if m.SupportedParameters != nil {
		model.SupportedParameters = make([]string, len(m.SupportedParameters))
		copy(model.SupportedParameters, m.SupportedParameters)
	}

	return model
}

// ToModels converts all models in the catalog to internal model types
func (c *Catalog) ToModels() []*models.Model {
	result := make([]*models.Model, 0, len(c.Models))
	for _, m := range c.Models {
		result = append(result, m.ToModel())
	}
	return result
}

// GetModelByID returns a model by its ID, or nil if not found
func (c *Catalog) GetModelByID(id string) *Model {
	return c.Models[id]
}

// GetModelsByProvider returns all models for a given provider
func (c *Catalog) GetModelsByProvider(provider string) []*Model {
	var result []*Model
	prefix := provider + "/"
	for id, model := range c.Models {
		if strings.HasPrefix(id, prefix) {
			result = append(result, model)
		}
	}
	return result
}

// ResolveModelAlias resolves a model ID to its canonical form
// If the model has a canonical_slug, returns that; otherwise returns the original ID
func (c *Catalog) ResolveModelAlias(modelID string) string {
	if model := c.GetModelByID(modelID); model != nil {
		if model.CanonicalSlug != "" {
			return model.CanonicalSlug
		}
		if model.Permaslug != "" {
			return model.Permaslug
		}
	}
	return modelID
}
