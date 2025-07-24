package models_test

import (
	"testing"
	"time"

	"github.com/agentstation/starport/pkg/models"
)

func TestModel_IsType(t *testing.T) {
	model := &models.Model{
		ID: "test-model",
		Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
	}

	if !model.IsChat() {
		t.Error("IsChat() returned false for chat model")
	}
	if model.IsEmbedding() {
		t.Error("IsEmbedding() returned true for chat model")
	}
	if model.IsImage() {
		t.Error("IsImage() returned true for chat model")
	}
	if model.IsAudio() {
		t.Error("IsAudio() returned true for chat model")
	}
	if model.IsModeration() {
		t.Error("IsModeration() returned true for chat model")
	}
}

func TestModel_IsActive(t *testing.T) {
	tests := []struct {
		name       string
		model      models.Model
		wantActive bool
	}{
		{
			name: "active model",
			model: models.Model{
				ID:         "active",
				Deprecated: false,
			},
			wantActive: true,
		},
		{
			name: "deprecated model",
			model: models.Model{
				ID:         "deprecated",
				Deprecated: true,
			},
			wantActive: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.model.IsActive(); got != tt.wantActive {
				t.Errorf("IsActive() = %v, want %v", got, tt.wantActive)
			}
		})
	}
}

func TestModel_HasFeature(t *testing.T) {
	model := &models.Model{
		ID:                  "test-model",
		SupportedParameters: []string{"reasoning", "code-execution", "web-search"},
	}

	tests := []struct {
		feature string
		want    bool
	}{
		{"reasoning", true},
		{"code-execution", true},
		{"web-search", true},
		{"nonexistent", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.feature, func(t *testing.T) {
			if got := model.HasFeature(tt.feature); got != tt.want {
				t.Errorf("HasFeature(%q) = %v, want %v", tt.feature, got, tt.want)
			}
		})
	}
}

func TestModel_CalculateCost(t *testing.T) {
	tests := []struct {
		name         string
		model        models.Model
		inputTokens  int
		outputTokens int
		expectedCost float64
	}{
		{
			name: "standard pricing",
			model: models.Model{
				Pricing: &models.Pricing{
					Prompt:     "0.00001",    // $0.00001 per token = $0.01 per 1K tokens = $10 per million
					Completion: "0.00003",    // $0.00003 per token = $0.03 per 1K tokens = $30 per million
				},
			},
			inputTokens:  1000,
			outputTokens: 500,
			expectedCost: 0.01 + 0.015, // 0.025
		},
		{
			name: "zero cost",
			model: models.Model{
				Pricing: &models.Pricing{
					Prompt:     "0",
					Completion: "0",
				},
			},
			inputTokens:  1000,
			outputTokens: 500,
			expectedCost: 0,
		},
		{
			name: "large token count",
			model: models.Model{
				Pricing: &models.Pricing{
					Prompt:     "0.000002",  // $0.000002 per token = $0.002 per 1K tokens = $2 per million
					Completion: "0.000006",  // $0.000006 per token = $0.006 per 1K tokens = $6 per million
				},
			},
			inputTokens:  100000,
			outputTokens: 50000,
			expectedCost: 0.2 + 0.3, // 0.5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := tt.model.CalculateCost(tt.inputTokens, tt.outputTokens)
			if cost != tt.expectedCost {
				t.Errorf("CalculateCost(%d, %d) = %v, want %v",
					tt.inputTokens, tt.outputTokens, cost, tt.expectedCost)
			}
		})
	}
}

func TestModel_Clone(t *testing.T) {
	original := &models.Model{
		ID:              "test-model",
		Name:            "Test Model",
		Provider:        "test-provider",
		ContextLength:   4096,
		Architecture: &models.Architecture{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
			Tokenizer:       "GPT",
			InstructType:    "chatml",
		},
		Pricing: &models.Pricing{
			Prompt:     "0.00001",
			Completion: "0.00003",
		},
		TopProvider: &models.TopProvider{
			IsModerated:         true,
			ContextLength:       4096,
			MaxCompletionTokens: 1024,
		},
		SupportedParameters: []string{"temperature", "top_p", "tools"},
		PerRequestLimits: map[string]interface{}{
			"rpm": 100,
			"tpm": 100000,
		},
		Deprecated:      false,
		DeprecatedAt:    time.Now(),
		ReplacedBy:      "new-model",
		Tags:            []string{"latest", "stable"},
		Created:         time.Now().Unix(),
		UpdatedAt:       time.Now(),
	}

	clone := original.Clone()

	// Verify it's a different instance
	if clone == original {
		t.Error("Clone() returned same instance")
	}

	// Verify all fields are copied
	if clone.ID != original.ID {
		t.Error("Clone() didn't copy ID")
	}
	if clone.Name != original.Name {
		t.Error("Clone() didn't copy Name")
	}
	if clone.Provider != original.Provider {
		t.Error("Clone() didn't copy Provider")
	}
	if clone.ContextLength != original.ContextLength {
		t.Error("Clone() didn't copy ContextLength")
	}
	// Verify Architecture is deep copied
	if clone.Architecture == nil || original.Architecture == nil {
		t.Error("Clone() didn't handle Architecture properly")
	} else {
		if clone.Architecture == original.Architecture {
			t.Error("Clone() didn't deep copy Architecture")
		}
		if clone.Architecture.Tokenizer != original.Architecture.Tokenizer {
			t.Error("Clone() didn't copy Architecture.Tokenizer")
		}
	}
	if clone.Deprecated != original.Deprecated {
		t.Error("Clone() didn't copy Deprecated")
	}
	if !clone.DeprecatedAt.Equal(original.DeprecatedAt) {
		t.Error("Clone() didn't copy DeprecatedAt")
	}
	if clone.ReplacedBy != original.ReplacedBy {
		t.Error("Clone() didn't copy ReplacedBy")
	}
	if clone.Created != original.Created {
		t.Error("Clone() didn't copy Created")
	}
	if !clone.UpdatedAt.Equal(original.UpdatedAt) {
		t.Error("Clone() didn't copy UpdatedAt")
	}

	// Verify slices are deep copied
	if len(clone.Tags) != len(original.Tags) {
		t.Error("Clone() didn't copy Tags correctly")
	}
	if &clone.Tags[0] == &original.Tags[0] {
		t.Error("Clone() didn't deep copy Tags")
	}

	// Modify clone and verify original is unchanged
	clone.Tags[0] = "modified"
	if original.Tags[0] == "modified" {
		t.Error("Modifying clone affected original Tags")
	}
}

func TestModel_Validation(t *testing.T) {
	tests := []struct {
		name    string
		model   models.Model
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid model",
			model: models.Model{
				ID:              "valid-model",
				Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
				Name:            "Valid Model",
				Provider:        "test-provider",
				ContextLength:   4096,
			},
			wantErr: false,
		},
		{
			name: "missing ID",
			model: models.Model{
				Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
				Name:            "Missing ID",
				Provider:        "test-provider",
				ContextLength:   4096,
			},
			wantErr: true,
			errMsg:  "ID is required",
		},
		{
			name: "missing architecture",
			model: models.Model{
				ID:              "missing-architecture",
				Name:            "Missing Architecture",
				Provider:        "test-provider",
				ContextLength:   4096,
			},
			wantErr: true,
			errMsg:  "Architecture is required",
		},
		{
			name: "negative context length",
			model: models.Model{
				ID:              "negative-context",
				Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
				Name:            "Negative Context",
				Provider:        "test-provider",
				ContextLength:   -1,
			},
			wantErr: true,
			errMsg:  "ContextLength must be positive",
		},
		{
			name: "valid with architecture only",
			model: models.Model{
				ID:              "arch-only",
				Name:            "Architecture Only",
				Provider:        "test-provider",
				ContextLength:   4096,
				Architecture: &models.Architecture{
					InputModalities:  []string{"text"},
					OutputModalities: []string{"text"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid pricing",
			model: models.Model{
				ID:              "invalid-pricing",
				Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
				Name:            "Invalid Pricing",
				Provider:        "test-provider",
				ContextLength:   4096,
				Pricing: &models.Pricing{
					Prompt:     "invalid",
					Completion: "0.00003",
				},
			},
			wantErr: true,
			errMsg:  "invalid prompt pricing: strconv.ParseFloat: parsing \"invalid\": invalid syntax",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.model.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && err.Error() != tt.errMsg {
				t.Errorf("Validate() error = %v, want %v", err.Error(), tt.errMsg)
			}
		})
	}
}


func TestModel_ToOpenAI(t *testing.T) {
	tests := []struct {
		name     string
		model    models.Model
		expected models.OpenAIModel
	}{
		{
			name: "basic conversion with provider",
			model: models.Model{
				ID:       "gpt-4",
				Created:  1678841600,
				Provider: "openai",
			},
			expected: models.OpenAIModel{
				ID:      "gpt-4",
				Object:  "model",
				Created: 1678841600,
				OwnedBy: "openai",
			},
		},
		{
			name: "derive owner from provider",
			model: models.Model{
				ID:       "test-model",
				Created:  1678841600,
				Provider: "anthropic",
			},
			expected: models.OpenAIModel{
				ID:      "test-model",
				Object:  "model",
				Created: 1678841600,
				OwnedBy: "anthropic",
			},
		},
		{
			name: "derive owner from ID",
			model: models.Model{
				ID:      "anthropic/claude-3-opus",
				Created: 1678841600,
			},
			expected: models.OpenAIModel{
				ID:      "anthropic/claude-3-opus",
				Object:  "model",
				Created: 1678841600,
				OwnedBy: "anthropic",
			},
		},
		{
			name: "default owner",
			model: models.Model{
				ID:      "model-without-provider",
				Created: 1678841600,
			},
			expected: models.OpenAIModel{
				ID:      "model-without-provider",
				Object:  "model",
				Created: 1678841600,
				OwnedBy: "system",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.model.ToOpenAI()
			if result.ID != tt.expected.ID {
				t.Errorf("ToOpenAI() ID = %v, want %v", result.ID, tt.expected.ID)
			}
			if result.Object != tt.expected.Object {
				t.Errorf("ToOpenAI() Object = %v, want %v", result.Object, tt.expected.Object)
			}
			if result.Created != tt.expected.Created {
				t.Errorf("ToOpenAI() Created = %v, want %v", result.Created, tt.expected.Created)
			}
			if result.OwnedBy != tt.expected.OwnedBy {
				t.Errorf("ToOpenAI() OwnedBy = %v, want %v", result.OwnedBy, tt.expected.OwnedBy)
			}
		})
	}
}

func TestModel_ToOpenRouter(t *testing.T) {
	model := &models.Model{
		ID:              "test-model",
		Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
		ContextLength:   4096,
		SupportedParameters: []string{"tools", "json_mode"},
	}

	result := model.ToOpenRouter()

	// Check that Architecture is derived
	if result.Architecture == nil {
		t.Error("ToOpenRouter() didn't derive Architecture")
	} else {
		if len(result.Architecture.InputModalities) != 1 || result.Architecture.InputModalities[0] != "text" {
			t.Errorf("ToOpenRouter() InputModalities = %v, expected [text]", result.Architecture.InputModalities)
		}
		if result.Architecture.OutputModalities[0] != "text" {
			t.Errorf("ToOpenRouter() OutputModalities = %v, expected [text]", result.Architecture.OutputModalities)
		}
	}

	// Check that TopProvider is populated
	if result.TopProvider == nil {
		t.Error("ToOpenRouter() didn't populate TopProvider")
	} else {
		if result.TopProvider.ContextLength != 4096 {
			t.Errorf("ToOpenRouter() TopProvider.ContextLength = %v, expected 4096", result.TopProvider.ContextLength)
		}
	}
}

func TestModel_GetInputCost(t *testing.T) {
	tests := []struct {
		name     string
		model    models.Model
		expected float64
	}{
		{
			name: "valid pricing",
			model: models.Model{
				Pricing: &models.Pricing{
					Prompt: "0.00001", // $0.00001 per token = $0.01 per 1K tokens
				},
			},
			expected: 0.01,
		},
		{
			name: "no pricing",
			model: models.Model{},
			expected: 0,
		},
		{
			name: "invalid pricing",
			model: models.Model{
				Pricing: &models.Pricing{
					Prompt: "invalid",
				},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.model.GetInputCost()
			if result != tt.expected {
				t.Errorf("GetInputCost() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestModel_HasVisionSupport(t *testing.T) {
	tests := []struct {
		name     string
		model    models.Model
		expected bool
	}{
		{
			name: "architecture with image modality",
			model: models.Model{
				Architecture: &models.Architecture{
					InputModalities: []string{"text", "image"},
				},
			},
			expected: true,
		},
		{
			name: "no vision support",
			model: models.Model{
				Architecture: &models.Architecture{
					InputModalities: []string{"text"},
				},
			},
			expected: false,
		},
		{
			name: "no architecture or flag",
			model: models.Model{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.model.HasVisionSupport()
			if result != tt.expected {
				t.Errorf("HasVisionSupport() = %v, want %v", result, tt.expected)
			}
		})
	}
}