package catalog

import (
	"testing"
	"time"
)

func TestMarkModelInvalid(t *testing.T) {
	// Clear any existing invalid models
	invalidMutex.Lock()
	invalidModels = make(map[string]time.Time)
	invalidMutex.Unlock()

	modelID := "anthropic/claude-4-opus"

	// Initially, model should not be invalid
	if IsModelInvalid(modelID) {
		t.Error("Model should not be invalid initially")
	}

	// Mark model as invalid
	MarkModelInvalid(modelID)

	// Now model should be invalid
	if !IsModelInvalid(modelID) {
		t.Error("Model should be invalid after marking")
	}

	// Test that invalid status is maintained
	time.Sleep(10 * time.Millisecond)
	if !IsModelInvalid(modelID) {
		t.Error("Model should remain invalid")
	}
}

func TestGetModelsByProviderWithDynamic_FiltersInvalid(t *testing.T) {
	// Clear state
	dynamicMutex.Lock()
	dynamicModels = make(map[string]*Model)
	dynamicMutex.Unlock()

	invalidMutex.Lock()
	invalidModels = make(map[string]time.Time)
	invalidMutex.Unlock()

	// Mark a model as invalid
	MarkModelInvalid("anthropic/claude-4-opus")

	// Get models - should filter out the invalid one
	models := GetModelsByProviderWithDynamic("anthropic")

	// Check that invalid model is not in the list
	for _, model := range models {
		if model.ID == "anthropic/claude-4-opus" {
			t.Error("Invalid model should be filtered out")
		}
	}
}
