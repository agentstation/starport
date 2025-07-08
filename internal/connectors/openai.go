package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OpenAIConnector implements the Connector interface for OpenAI
type OpenAIConnector struct {
	OpenAICompatibleConnector
}

// NewOpenAIConnector creates a new OpenAI connector
func NewOpenAIConnector(config ProviderConfig) (*OpenAIConnector, error) {
	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
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

	return &OpenAIConnector{
		OpenAICompatibleConnector: OpenAICompatibleConnector{
			config:     config,
			provider:   "openai",
			httpClient: &http.Client{
				Transport: transport,
				Timeout:   config.Timeout,
			},
		},
	}, nil
}

// Name returns the provider name
func (c *OpenAIConnector) Name() string {
	return "openai"
}

// Chat performs a chat completion request
func (c *OpenAIConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return c.OpenAICompatibleConnector.Chat(ctx, req, c.setHeaders, c.handleError)
}

// ChatStream performs a streaming chat completion request
func (c *OpenAIConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	return c.OpenAICompatibleConnector.ChatStream(ctx, req, c.setHeaders, c.handleError, newOpenAICompatibleStream)
}

// Embeddings generates embeddings for the given input
func (c *OpenAIConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	return c.OpenAICompatibleConnector.Embeddings(ctx, req, c.setHeaders, c.handleError)
}

// Models lists available models from the provider
func (c *OpenAIConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	return c.OpenAICompatibleConnector.Models(ctx, c.setHeaders, c.handleError)
}

// Health checks the health of the connector
func (c *OpenAIConnector) Health(ctx context.Context) error {
	// Use models endpoint as health check
	_, err := c.Models(ctx)
	if err != nil {
		// If it's an auth error, the service is up
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusUnauthorized {
			return nil
		}
		return fmt.Errorf("%w: %v", ErrHealthCheckFailed, err)
	}
	return nil
}

// Close closes the connector
func (c *OpenAIConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// setHeaders sets OpenAI-specific headers
func (c *OpenAIConnector) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
}

// handleError handles OpenAI-specific error responses
func (c *OpenAIConnector) handleError(resp *http.Response) error {
	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    "failed to decode error response",
			Provider:   "openai",
		}
	}


	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    errResp.Error.Message,
		Type:       errResp.Error.Type,
		Code:       errResp.Error.Code,
		Provider:   "openai",
	}
}

