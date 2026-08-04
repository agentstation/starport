package connectors

import (
	"context"
	"fmt"
	"net/http"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// AzureOpenAIConnector implements the Connector interface for Azure OpenAI
type AzureOpenAIConnector struct {
	*OpenAIConnector
}

// NewAzureOpenAIConnector creates a new Azure OpenAI connector
func NewAzureOpenAIConnector(config ProviderConfig) (*AzureOpenAIConnector, error) {
	if config.BaseURL == "" {
		return nil, fmt.Errorf("azure openai requires a catalog-derived or operator-supplied base URL")
	}

	// Create an OpenAI connector with Azure's base URL
	openAIConnector, err := NewOpenAIConnector(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure openai connector: %w", err)
	}
	openAIConnector.providerID = catalogs.ProviderIDAzureOpenAI
	openAIConnector.provider = AzureOpenAIProvider

	return &AzureOpenAIConnector{
		OpenAIConnector: openAIConnector,
	}, nil
}

// Name returns the provider name
func (c *AzureOpenAIConnector) Name() string {
	return AzureOpenAIProvider
}

// Chat performs a chat completion request
func (c *AzureOpenAIConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return c.OpenAICompatibleConnector.Chat(ctx, req, c.setHeaders, c.handleError)
}

// ChatStream performs a streaming chat completion request
func (c *AzureOpenAIConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	return c.OpenAICompatibleConnector.ChatStream(ctx, req, c.setHeaders, c.handleError, newOpenAICompatibleStream)
}

// Embeddings generates embeddings for the given input
func (c *AzureOpenAIConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	return c.OpenAICompatibleConnector.Embeddings(ctx, req, c.setHeaders, c.handleError)
}

// setHeaders sets Azure-specific headers
func (c *AzureOpenAIConnector) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	applyRegisteredInferenceAuth(catalogs.ProviderIDAzureOpenAI, req, c.config.APIKey)
	req.Header.Set("User-Agent", "starport/1.0")
}

// handleError handles error responses from the API (reuse OpenAI's error handling)
func (c *AzureOpenAIConnector) handleError(resp *http.Response) error {
	return c.OpenAIConnector.handleError(resp)
}
