package providers_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/agentstation/starport/pkg/models"
	"github.com/agentstation/starport/pkg/providers"
)

// mockConnector implements the Connector interface for testing
type mockConnector struct {
	chatFunc       func(ctx context.Context, model string, req *providers.ChatRequest) (*providers.ChatResponse, error)
	chatStreamFunc func(ctx context.Context, model string, req *providers.ChatRequest) (providers.ChatStream, error)
	embeddingsFunc func(ctx context.Context, model string, req *providers.EmbeddingsRequest) (*providers.EmbeddingsResponse, error)
}

func (m *mockConnector) Chat(ctx context.Context, model string, req *providers.ChatRequest) (*providers.ChatResponse, error) {
	if m.chatFunc != nil {
		return m.chatFunc(ctx, model, req)
	}
	return &providers.ChatResponse{
		ID:      "test-response",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: providers.Message{
					Role:    "assistant",
					Content: "Test response",
				},
				FinishReason: "stop",
			},
		},
		Usage: providers.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}, nil
}

func (m *mockConnector) ChatStream(ctx context.Context, model string, req *providers.ChatRequest) (providers.ChatStream, error) {
	if m.chatStreamFunc != nil {
		return m.chatStreamFunc(ctx, model, req)
	}
	return &mockChatStream{}, nil
}

func (m *mockConnector) Embeddings(ctx context.Context, model string, req *providers.EmbeddingsRequest) (*providers.EmbeddingsResponse, error) {
	if m.embeddingsFunc != nil {
		return m.embeddingsFunc(ctx, model, req)
	}
	return &providers.EmbeddingsResponse{
		Object: "list",
		Model:  model,
		Data: []providers.Embedding{
			{
				Object:    "embedding",
				Index:     0,
				Embedding: []float64{0.1, 0.2, 0.3},
			},
		},
		Usage: providers.Usage{
			PromptTokens: 5,
			TotalTokens:  5,
		},
	}, nil
}

// mockChatStream implements the ChatStream interface
type mockChatStream struct {
	chunks []providers.ChatChunk
	index  int
	closed bool
}

func (s *mockChatStream) Next() (*providers.ChatChunk, error) {
	if s.closed {
		return nil, errors.New("stream closed")
	}
	if s.index >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return &chunk, nil
}

func (s *mockChatStream) Close() error {
	s.closed = true
	return nil
}

func TestProvider_ModelManagement(t *testing.T) {
	provider := &providers.Provider{
		ID:      "test-provider",
		Name:    "Test Provider",
		Type:    "openai",
		Enabled: true,
		BaseURL: "https://api.test.com",
		APIKey:  "test-key",
	}

	// Test adding models
	model1 := &models.Model{
		ID: "gpt-4",
		Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
		ContextLength: 128000,
	}

	err := provider.AddModel(model1)
	if err != nil {
		t.Errorf("AddModel() error = %v", err)
	}

	// Test getting model
	retrieved, err := provider.GetModel("gpt-4")
	if err != nil {
		t.Errorf("GetModel() error = %v", err)
	}
	if retrieved.ID != model1.ID {
		t.Errorf("GetModel() returned wrong model")
	}

	// Test model not found
	_, err = provider.GetModel("nonexistent")
	if !errors.Is(err, providers.ErrModelNotFound) {
		t.Errorf("GetModel() expected ErrModelNotFound, got %v", err)
	}

	// Test listing models
	modelList := provider.ListModels()
	if len(modelList) != 1 {
		t.Errorf("ListModels() returned %d models, expected 1", len(modelList))
	}

	// Test listing active models
	activeModels := provider.ListActiveModels()
	if len(activeModels) != 1 {
		t.Errorf("ListActiveModels() returned %d models, expected 1", len(activeModels))
	}
}

func TestProvider_WithEmbeddedConnector(t *testing.T) {
	// Create provider with embedded connector
	provider := &providers.Provider{
		ID:      "test-provider",
		Name:    "Test Provider",
		Type:    "test",
		Enabled: true,
		BaseURL: "https://api.test.com",
		APIKey:  "test-key",
	}

	// Add chat model
	provider.AddModel(&models.Model{
		ID: "test-model",
		Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
		ContextLength: 4096,
	})

	// Embed connector
	provider.Connector = &mockConnector{}

	// Test Chat through embedded connector
	ctx := context.Background()
	req := &providers.ChatRequest{
		Model: "test-model",
		Messages: []providers.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	resp, err := provider.Chat(ctx, "test-model", req)
	if err != nil {
		t.Errorf("Chat() error = %v", err)
	}
	if resp == nil {
		t.Error("Chat() returned nil response")
	}
}

func TestRegistry(t *testing.T) {
	registry := providers.NewRegistry()

	// Create test providers
	provider1 := &providers.Provider{
		ID:        "provider1",
		Name:      "Provider 1",
		Type:      "openai",
		Enabled:   true,
		Connector: &mockConnector{},
	}

	provider2 := &providers.Provider{
		ID:        "provider2",
		Name:      "Provider 2",
		Type:      "anthropic",
		Enabled:   false,
		Connector: &mockConnector{},
	}

	provider3 := &providers.Provider{
		ID:        "provider3",
		Name:      "Provider 3",
		Type:      "openai",
		Enabled:   true,
		Connector: &mockConnector{},
	}

	// Test adding providers
	if err := registry.Add(provider1); err != nil {
		t.Errorf("Add() error = %v", err)
	}
	if err := registry.Add(provider2); err != nil {
		t.Errorf("Add() error = %v", err)
	}
	if err := registry.Add(provider3); err != nil {
		t.Errorf("Add() error = %v", err)
	}

	// Test adding nil provider
	if err := registry.Add(nil); err == nil {
		t.Error("Add(nil) should return error")
	}

	// Test getting provider
	p, err := registry.Get("provider1")
	if err != nil {
		t.Errorf("Get() error = %v", err)
	}
	if p.ID != "provider1" {
		t.Errorf("Get() returned wrong provider")
	}

	// Test getting disabled provider
	_, err = registry.Get("provider2")
	if !errors.Is(err, providers.ErrProviderDisabled) {
		t.Errorf("Get() expected ErrProviderDisabled, got %v", err)
	}

	// Test getting nonexistent provider
	_, err = registry.Get("nonexistent")
	if !errors.Is(err, providers.ErrProviderNotFound) {
		t.Errorf("Get() expected ErrProviderNotFound, got %v", err)
	}

	// Test listing providers
	all := registry.List()
	if len(all) != 3 {
		t.Errorf("List() returned %d providers, expected 3", len(all))
	}

	enabled := registry.ListEnabled()
	if len(enabled) != 2 {
		t.Errorf("ListEnabled() returned %d providers, expected 2", len(enabled))
	}

	byType := registry.ListByType("openai")
	if len(byType) != 2 {
		t.Errorf("ListByType() returned %d providers, expected 2", len(byType))
	}

	// Test count methods
	if count := registry.Count(); count != 3 {
		t.Errorf("Count() = %d, expected 3", count)
	}

	if count := registry.CountEnabled(); count != 2 {
		t.Errorf("CountEnabled() = %d, expected 2", count)
	}

	// Test Has
	if !registry.Has("provider1") {
		t.Error("Has() returned false for existing provider")
	}
	if registry.Has("nonexistent") {
		t.Error("Has() returned true for nonexistent provider")
	}

	// Test Remove
	if err := registry.Remove("provider1"); err != nil {
		t.Errorf("Remove() error = %v", err)
	}
	if registry.Has("provider1") {
		t.Error("Provider still exists after Remove()")
	}

	// Test removing nonexistent provider
	if err := registry.Remove("nonexistent"); !errors.Is(err, providers.ErrProviderNotFound) {
		t.Errorf("Remove() expected ErrProviderNotFound, got %v", err)
	}
}

func TestModel_Capabilities(t *testing.T) {
	model := &models.Model{
		ID: "test-model",
		Architecture: &models.Architecture{
			InputModalities:  []string{"text"},
			OutputModalities: []string{"text"},
		},
		ContextLength: 4096,
		Pricing: &models.Pricing{
			Prompt:     "0.00001", // $0.01 per 1K tokens
			Completion: "0.00003", // $0.03 per 1K tokens
		},
		SupportedParameters: []string{"reasoning", "code-execution"},
	}

	// Test type checking
	if !model.IsChat() {
		t.Error("IsChat() returned false for chat model")
	}
	if model.IsEmbedding() {
		t.Error("IsEmbedding() returned true for chat model")
	}

	// Test feature checking
	if !model.HasFeature("reasoning") {
		t.Error("HasFeature() returned false for existing feature")
	}
	if model.HasFeature("nonexistent") {
		t.Error("HasFeature() returned true for nonexistent feature")
	}

	// Test cost calculation
	cost := model.CalculateCost(1000, 500)
	expectedCost := 0.01 + (0.5 * 0.03)
	if cost != expectedCost {
		t.Errorf("CalculateCost() = %v, expected %v", cost, expectedCost)
	}
}

func TestAPIError(t *testing.T) {
	err := &providers.APIError{
		Provider:   "test",
		StatusCode: 500,
		Message:    "Internal server error",
		Details:    "Connection timeout",
	}

	// Test error message
	expected := "Internal server error: Connection timeout"
	if err.Error() != expected {
		t.Errorf("Error() = %v, expected %v", err.Error(), expected)
	}

	// Test retryable
	if !err.IsRetryable() {
		t.Error("IsRetryable() returned false for 500 error")
	}

	err.StatusCode = 400
	if err.IsRetryable() {
		t.Error("IsRetryable() returned true for 400 error")
	}

	err.StatusCode = 429
	if !err.IsRetryable() {
		t.Error("IsRetryable() returned false for 429 error")
	}
}

func TestMultiError(t *testing.T) {
	multi := &providers.MultiError{}

	// Test empty
	if multi.HasErrors() {
		t.Error("HasErrors() returned true for empty MultiError")
	}
	if multi.First() != nil {
		t.Error("First() returned non-nil for empty MultiError")
	}

	// Add errors
	multi.Add(errors.New("error 1"))
	multi.Add(errors.New("error 2"))
	multi.Add(nil) // Should be ignored

	if !multi.HasErrors() {
		t.Error("HasErrors() returned false after adding errors")
	}
	if len(multi.Errors) != 2 {
		t.Errorf("Expected 2 errors, got %d", len(multi.Errors))
	}
	if multi.First().Error() != "error 1" {
		t.Errorf("First() returned wrong error")
	}
}
