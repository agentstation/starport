package connectors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GroqConnector implements the Connector interface for Groq
// Groq provides an OpenAI-compatible API, so we can reuse the OpenAI connector
type GroqConnector struct {
	*OpenAIConnector
}

// NewGroqConnector creates a new Groq connector
func NewGroqConnector(config ProviderConfig) (*GroqConnector, error) {
	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = "https://api.groq.com/openai/v1"
	}

	// Create an OpenAI connector with Groq's base URL
	openAIConnector, err := NewOpenAIConnector(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create groq connector: %w", err)
	}

	return &GroqConnector{
		OpenAIConnector: openAIConnector,
	}, nil
}

// Name returns the provider name
func (c *GroqConnector) Name() string {
	return "groq"
}

// Models returns Groq-specific models
func (c *GroqConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	return fetchModelsWithCache(ctx, "groq", func(ctx context.Context) (*ModelsResponse, error) {
		// Try to fetch models dynamically from Groq API
		req, err := http.NewRequestWithContext(ctx, "GET", c.config.BaseURL+"/openai/v1/models", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Set headers
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		// Parse the response
		models, err := parseModelsResponse(body, "groq")
		if err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		return models, nil
	})
}

// staticModelsList returns the hardcoded list of models as fallback
func (c *GroqConnector) staticModelsList() *ModelsResponse {
	models := []Model{
		// Production models
		{
			ID:      "groq/llama-3.1-8b-instant",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "meta",
		},
		{
			ID:      "groq/llama-3.3-70b-versatile",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "meta",
		},
		{
			ID:      "groq/gemma2-9b-it",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Preview models (include the most relevant ones for LLM use)
		{
			ID:      "groq/deepseek-r1-distill-llama-70b",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "deepseek/meta",
		},
		// Legacy models (still supported)
		{
			ID:      "groq/llama3-8b-8192",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "meta",
		},
		{
			ID:      "groq/llama3-70b-8192",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "meta",
		},
	}

	return &ModelsResponse{
		Object: "list",
		Data:   models,
	}
}

// Embeddings returns an error as Groq doesn't support embeddings
func (c *GroqConnector) Embeddings(_ context.Context, _ *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	return nil, &APIError{
		StatusCode: http.StatusNotImplemented,
		Message:    "Groq does not support embeddings. Consider using a different provider for embedding generation.",
		Type:       "not_supported",
		Provider:   "groq",
	}
}

// Health checks the health of the connector
func (c *GroqConnector) Health(ctx context.Context) error {
	// Try a minimal chat request
	req := &ChatRequest{
		Model: "groq/llama-3.1-8b-instant",
		Messages: []Message{
			{Role: "user", Content: "Hi"},
		},
		MaxTokens: intPtr(1),
	}

	_, err := c.Chat(ctx, req)
	if err != nil {
		// Check if it's an auth error (which means the service is up)
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusUnauthorized {
			return nil // Service is up, just no valid key
		}
		return err
	}
	return nil
}