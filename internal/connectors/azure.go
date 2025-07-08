package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AzureOpenAIConnector implements the Connector interface for Azure OpenAI
type AzureOpenAIConnector struct {
	*OpenAIConnector
	apiVersion string
}

// NewAzureOpenAIConnector creates a new Azure OpenAI connector
func NewAzureOpenAIConnector(config ProviderConfig) (*AzureOpenAIConnector, error) {
	// Azure requires a base URL with the resource name
	if config.BaseURL == "" {
		return nil, fmt.Errorf("azure openai requires base URL with resource name (e.g., https://myresource.openai.azure.com)")
	}

	// Get API version from config or use default
	apiVersion := "2024-02-01"
	if version, ok := config.Extra["api_version"].(string); ok {
		apiVersion = version
	}

	// Create an OpenAI connector with Azure's base URL
	openAIConnector, err := NewOpenAIConnector(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure openai connector: %w", err)
	}

	return &AzureOpenAIConnector{
		OpenAIConnector: openAIConnector,
		apiVersion:      apiVersion,
	}, nil
}

// Name returns the provider name
func (c *AzureOpenAIConnector) Name() string {
	return "azure"
}

// Chat performs a chat completion request
func (c *AzureOpenAIConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Azure uses deployment names instead of model names
	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Azure endpoint format: /openai/deployments/{deployment-id}/chat/completions
	// Strip the "azure/" prefix from model name to get deployment name
	deployment := strings.TrimPrefix(req.Model, "azure/")
	endpoint := fmt.Sprintf("/openai/deployments/%s/chat/completions", deployment)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(httpReq)

	resp, err := doRequestWithRetry(c.httpClient, httpReq, c.config)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResp, nil
}

// ChatStream performs a streaming chat completion request
func (c *AzureOpenAIConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Strip the "azure/" prefix from model name to get deployment name
	deployment := strings.TrimPrefix(req.Model, "azure/")
	endpoint := fmt.Sprintf("/openai/deployments/%s/chat/completions", deployment)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := doRequestWithRetry(c.httpClient, httpReq, c.config)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, c.handleError(resp)
	}

	// Reuse OpenAI stream since Azure uses the same SSE format
	return newOpenAICompatibleStream(resp), nil
}

// Embeddings generates embeddings for the given input
func (c *AzureOpenAIConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Strip the "azure/" prefix from model name to get deployment name
	deployment := strings.TrimPrefix(req.Model, "azure/")
	endpoint := fmt.Sprintf("/openai/deployments/%s/embeddings", deployment)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(httpReq)

	resp, err := doRequestWithRetry(c.httpClient, httpReq, c.config)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var embResp EmbeddingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &embResp, nil
}

// Models lists available models/deployments
func (c *AzureOpenAIConnector) Models(_ context.Context) (*ModelsResponse, error) {
	// Azure doesn't have a models endpoint. Deployments are customer-specific.
	// Return common deployment examples with a note.
	return &ModelsResponse{
		Object: "list",
		Data: []Model{
			{
				ID:      "azure/gpt-35-turbo",
				Object:  "model",
				Created: 1234567890,
				OwnedBy: "azure-openai",
			},
			{
				ID:      "azure/gpt-4",
				Object:  "model",
				Created: 1234567890,
				OwnedBy: "azure-openai",
			},
			{
				ID:      "azure/text-embedding-ada-002",
				Object:  "model",
				Created: 1234567890,
				OwnedBy: "azure-openai",
			},
			{
				ID:      "azure/YOUR-DEPLOYMENT-NAME",
				Object:  "model",
				Created: 1234567890,
				OwnedBy: "azure-openai",
			},
		},
	}, nil
}

// setHeaders sets Azure-specific headers
func (c *AzureOpenAIConnector) setHeaders(req *http.Request) {
	// Azure uses api-key header instead of Authorization
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.config.APIKey)
	req.Header.Set("User-Agent", "starport/1.0")
	
	// Add API version to query parameters
	q := req.URL.Query()
	q.Set("api-version", c.apiVersion)
	req.URL.RawQuery = q.Encode()
}

// handleError handles error responses from the API (reuse OpenAI's error handling)
func (c *AzureOpenAIConnector) handleError(resp *http.Response) error {
	return c.OpenAIConnector.handleError(resp)
}