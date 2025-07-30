//go:build integration
// +build integration

package catalog

import (
	"testing"
)

// TestCatalogIntegration tests the catalog with real data
func TestCatalogIntegration(t *testing.T) {
	catalog, err := GetCatalog()
	if err != nil {
		t.Fatalf("Failed to load catalog: %v", err)
	}

	// Verify we have a good number of models
	if len(catalog.Models) < 50 {
		t.Errorf("Expected at least 50 models in catalog, got %d", len(catalog.Models))
	}

	// Test some known models exist with proper metadata
	knownModels := []struct {
		id              string
		minContext      int
		hasPricing      bool
		hasArchitecture bool
	}{
		{
			id:              "anthropic/claude-3-opus",
			minContext:      100000,
			hasPricing:      true,
			hasArchitecture: true,
		},
		{
			id:              "openai/gpt-4",
			minContext:      8000,
			hasPricing:      true,
			hasArchitecture: true,
		},
		{
			id:              "google-aistudio/gemini-1.5-pro",
			minContext:      1000000,
			hasPricing:      true,
			hasArchitecture: true,
		},
	}

	for _, km := range knownModels {
		model := catalog.GetModelByID(km.id)
		if model == nil {
			t.Errorf("Expected model %s to exist", km.id)
			continue
		}

		if model.ContextLength < km.minContext {
			t.Errorf("Model %s: expected context >= %d, got %d",
				km.id, km.minContext, model.ContextLength)
		}

		if km.hasPricing && model.Pricing == nil {
			t.Errorf("Model %s: expected pricing information", km.id)
		}

		if km.hasArchitecture && model.Architecture == nil {
			t.Errorf("Model %s: expected architecture information", km.id)
		}
	}

	// Test provider grouping
	providers := []string{"anthropic", "openai", "groq", "mistral"}
	for _, provider := range providers {
		models := catalog.GetModelsByProvider(provider)
		if len(models) == 0 {
			t.Errorf("Expected at least one model for provider %s", provider)
		}
	}
}
