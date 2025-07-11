package connectors

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// GoogleAIStudioConnector implements the Connector interface for Google AI Studio (Gemini)
type GoogleAIStudioConnector struct {
	googleBaseConnector
}

// NewGoogleAIStudioConnector creates a new Google AI Studio connector
func NewGoogleAIStudioConnector(config ProviderConfig) (*GoogleAIStudioConnector, error) {
	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        config.MaxConnections,
		MaxIdleConnsPerHost: config.MaxConnections,
		IdleConnTimeout:     90 * time.Second,
	}

	return &GoogleAIStudioConnector{
		googleBaseConnector: googleBaseConnector{
			config: config,
			httpClient: &http.Client{
				Transport: transport,
				Timeout:   config.Timeout,
			},
			name: GoogleAIStudioProvider,
		},
	}, nil
}

// Name returns the provider name
func (c *GoogleAIStudioConnector) Name() string {
	return GoogleAIStudioProvider
}

// Chat performs a chat completion request
func (c *GoogleAIStudioConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return c.googleBaseConnector.Chat(ctx, req, c.getEndpoint, c.setHeaders)
}

// ChatStream performs a streaming chat completion request
func (c *GoogleAIStudioConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	return c.googleBaseConnector.ChatStream(ctx, req, c.getEndpoint, c.setHeaders)
}

// Embeddings generates embeddings for the given input
func (c *GoogleAIStudioConnector) Embeddings(_ context.Context, _ *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Google AI Studio (Gemini) doesn't support embeddings yet
	return nil, fmt.Errorf("embeddings not supported by Google AI Studio")
}

// Models lists available models from the provider
func (c *GoogleAIStudioConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	// Use cached models if available
	return fetchModelsWithCache(ctx, GoogleAIStudioProvider, func(ctx context.Context) (*ModelsResponse, error) {
		// Try to fetch models dynamically from Gemini API
		req, err := http.NewRequestWithContext(ctx, "GET", c.config.BaseURL+"/models", nil)
		if err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		c.setHeaders(req)

		resp, err := doRequestWithRetry(c.httpClient, req, c.config)
		if err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		body, err := readResponseBody(resp)
		if err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		// Parse the response
		models, err := parseModelsResponse(body, GoogleAIStudioProvider)
		if err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		// Filter to only include generative models
		filteredModels := &ModelsResponse{
			Object: "list",
			Data:   []Model{},
		}

		for _, model := range models.Data {
			// Only include generative models (gemini-*)
			if strings.Contains(model.ID, "gemini") {
				filteredModels.Data = append(filteredModels.Data, model)
			}
		}

		if len(filteredModels.Data) == 0 {
			// Fall back to static list if no models found
			return c.staticModelsList(), nil
		}

		return filteredModels, nil
	})
}

// staticModelsList returns the hardcoded list of Google AI Studio models
func (c *GoogleAIStudioConnector) staticModelsList() *ModelsResponse {
	providerPrefix := GoogleAIStudioProvider

	// Google AI Studio models (Gemini models only)
	// Updated: 2024-12-15
	models := []Model{
		{
			ID:      providerPrefix + "/gemini-1.5-pro",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/gemini-1.5-pro-latest",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/gemini-1.5-pro-002",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/gemini-1.5-flash",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/gemini-1.5-flash-latest",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/gemini-1.5-flash-002",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/gemini-1.0-pro",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/gemini-1.0-pro-latest",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/gemini-1.0-pro-vision-latest",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
	}

	return &ModelsResponse{
		Object: "list",
		Data:   models,
	}
}

// Health checks the health of the connector
func (c *GoogleAIStudioConnector) Health(ctx context.Context) error {
	// Simple health check - try to get response with minimal request
	req := &ChatRequest{
		Model: GoogleAIStudioProvider + "/gemini-1.5-flash",
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

// Close cleans up any resources
func (c *GoogleAIStudioConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Helper methods

func (c *GoogleAIStudioConnector) getEndpoint(model string, streaming bool) string {
	action := generateContentAction
	if streaming {
		action = streamGenerateContentAction
	}

	// Strip provider prefix from model name
	model = strings.TrimPrefix(model, GoogleAIStudioProvider+"/")
	model = strings.TrimPrefix(model, "google/") // Support legacy prefix
	return fmt.Sprintf("%s/models/%s:%s?key=%s", c.config.BaseURL, model, action, c.config.APIKey)
}

func (c *GoogleAIStudioConnector) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "starport/1.0")
}
