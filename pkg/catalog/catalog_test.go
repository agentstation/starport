package catalog

import (
	"testing"
)

func TestLoadEmbeddedCatalog(t *testing.T) {
	// Test loading the embedded catalog
	catalog, err := LoadEmbeddedCatalog()
	if err != nil {
		t.Fatalf("Failed to load embedded catalog: %v", err)
	}

	// Verify catalog is not nil
	if catalog == nil {
		t.Fatal("Expected non-nil catalog")
	}

	// Verify models map exists
	if catalog.Models == nil {
		t.Fatal("Expected models map to be initialized")
	}

	// Verify we have some models
	if len(catalog.Models) == 0 {
		t.Fatal("Expected catalog to contain models")
	}

	// Test a known model exists
	testModelID := "anthropic/claude-3-haiku"
	model, exists := catalog.Models[testModelID]
	if !exists {
		t.Errorf("Expected model %s to exist in catalog", testModelID)
	}

	if model != nil {
		// Verify model fields
		if model.ID != testModelID {
			t.Errorf("Expected model ID %s, got %s", testModelID, model.ID)
		}

		if model.Name == "" {
			t.Error("Expected model to have a name")
		}

		if model.ContextLength <= 0 {
			t.Error("Expected model to have positive context length")
		}
	}
}

func TestGetCatalog(t *testing.T) {
	// First call should load the catalog
	catalog1, err := GetCatalog()
	if err != nil {
		t.Fatalf("Failed to get catalog: %v", err)
	}

	if catalog1 == nil {
		t.Fatal("Expected non-nil catalog")
	}

	// Second call should return the same instance (singleton)
	catalog2, err := GetCatalog()
	if err != nil {
		t.Fatalf("Failed to get catalog on second call: %v", err)
	}

	// Verify same instance
	if catalog1 != catalog2 {
		t.Error("Expected GetCatalog to return the same instance (singleton pattern)")
	}
}

func TestCatalogGetModelByID(t *testing.T) {
	catalog, err := GetCatalog()
	if err != nil {
		t.Fatalf("Failed to get catalog: %v", err)
	}

	tests := []struct {
		name     string
		modelID  string
		wantNil  bool
	}{
		{
			name:     "existing model",
			modelID:  "anthropic/claude-3-haiku",
			wantNil:  false,
		},
		{
			name:     "non-existing model",
			modelID:  "fake/model-does-not-exist",
			wantNil:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := catalog.GetModelByID(tt.modelID)
			if tt.wantNil && model != nil {
				t.Errorf("Expected nil for model ID %s, got %+v", tt.modelID, model)
			}
			if !tt.wantNil && model == nil {
				t.Errorf("Expected model for ID %s, got nil", tt.modelID)
			}
		})
	}
}

func TestCatalogGetModelsByProvider(t *testing.T) {
	catalog, err := GetCatalog()
	if err != nil {
		t.Fatalf("Failed to get catalog: %v", err)
	}

	tests := []struct {
		name          string
		provider      string
		wantMinCount  int
	}{
		{
			name:          "anthropic provider",
			provider:      "anthropic",
			wantMinCount:  1, // Should have at least one Anthropic model
		},
		{
			name:          "openai provider",
			provider:      "openai",
			wantMinCount:  1, // Should have at least one OpenAI model
		},
		{
			name:          "non-existing provider",
			provider:      "fakeprovider",
			wantMinCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := catalog.GetModelsByProvider(tt.provider)
			if len(models) < tt.wantMinCount {
				t.Errorf("Expected at least %d models for provider %s, got %d", 
					tt.wantMinCount, tt.provider, len(models))
			}

			// Verify all returned models belong to the provider
			for _, model := range models {
				if model.ID[:len(tt.provider)] != tt.provider {
					t.Errorf("Model %s does not belong to provider %s", model.ID, tt.provider)
				}
			}
		})
	}
}

func TestModelToModelConversion(t *testing.T) {
	// Create a test catalog model
	catalogModel := &Model{
		ID:            "test/model",
		Name:          "Test Model",
		Created:       1234567890,
		Description:   "Test description",
		ContextLength: 4096,
		Architecture: &Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
			Tokenizer:        "test-tokenizer",
		},
		Pricing: &Pricing{
			Prompt:     "0.001",
			Completion: "0.002",
		},
		TopProvider: &TopProvider{
			MaxCompletionTokens: 2048,
			ContextLength:       4096,
		},
		SupportedParameters: []string{"temperature", "max_tokens"},
	}

	// Convert to internal model
	model := catalogModel.ToModel()

	// Verify conversion
	if model.ID != catalogModel.ID {
		t.Errorf("Expected ID %s, got %s", catalogModel.ID, model.ID)
	}

	if model.Name != catalogModel.Name {
		t.Errorf("Expected Name %s, got %s", catalogModel.Name, model.Name)
	}

	if model.Created != catalogModel.Created {
		t.Errorf("Expected Created %d, got %d", catalogModel.Created, model.Created)
	}

	if model.ContextLength != int64(catalogModel.ContextLength) {
		t.Errorf("Expected ContextLength %d, got %d", catalogModel.ContextLength, model.ContextLength)
	}

	if model.Provider != "test" {
		t.Errorf("Expected Provider 'test', got %s", model.Provider)
	}

	// Verify architecture conversion
	if model.Architecture == nil {
		t.Fatal("Expected non-nil Architecture")
	}

	if len(model.Architecture.InputModalities) != 1 || model.Architecture.InputModalities[0] != "text" {
		t.Error("Architecture input modalities not converted correctly")
	}

	// Verify pricing conversion
	if model.Pricing == nil {
		t.Fatal("Expected non-nil Pricing")
	}

	if model.Pricing.Prompt != "0.001" {
		t.Errorf("Expected Prompt pricing 0.001, got %s", model.Pricing.Prompt)
	}

	// Verify parameters
	if len(model.SupportedParameters) != 2 {
		t.Errorf("Expected 2 supported parameters, got %d", len(model.SupportedParameters))
	}
}