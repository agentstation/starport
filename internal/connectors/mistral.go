package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// MistralConnector implements the Connector interface for Mistral
type MistralConnector struct {
	OpenAICompatibleConnector
}

// NewMistralConnector creates a new Mistral connector
func NewMistralConnector(config ProviderConfig) (*MistralConnector, error) {
	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = "https://api.mistral.ai/v1"
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

	return &MistralConnector{
		OpenAICompatibleConnector: OpenAICompatibleConnector{
			config:     config,
			provider:   "mistral",
			httpClient: &http.Client{
				Transport: transport,
				Timeout:   config.Timeout,
			},
		},
	}, nil
}

// Name returns the provider name
func (c *MistralConnector) Name() string {
	return "mistral"
}

// Chat performs a chat completion request
func (c *MistralConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return c.OpenAICompatibleConnector.Chat(ctx, req, c.setHeaders, c.handleError)
}

// ChatStream performs a streaming chat completion request
func (c *MistralConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	return c.OpenAICompatibleConnector.ChatStream(ctx, req, c.setHeaders, c.handleError, newOpenAICompatibleStream)
}

// Embeddings generates embeddings for the given input
func (c *MistralConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	return c.OpenAICompatibleConnector.Embeddings(ctx, req, c.setHeaders, c.handleError)
}

// Models lists available models from the provider
func (c *MistralConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	return c.OpenAICompatibleConnector.Models(ctx, c.setHeaders, c.handleError)
}

// Health checks the health of the connector
func (c *MistralConnector) Health(ctx context.Context) error {
	// Use a minimal chat request as health check for Mistral
	resp, err := c.Chat(ctx, &ChatRequest{
		Model:     "mistral/mistral-tiny",
		Messages:  []Message{{Role: RoleUser, Content: "Hi"}},
		MaxTokens: intPtr(1),
	})
	if err != nil {
		// If it's an auth error, the service is up
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusUnauthorized {
			return nil
		}
		return fmt.Errorf("%w: %v", ErrHealthCheckFailed, err)
	}
	_ = resp // resp is intentionally unused
	return nil
}

// Close closes the connector
func (c *MistralConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// setHeaders sets Mistral-specific headers
func (c *MistralConnector) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
}

// handleError handles Mistral-specific error responses
func (c *MistralConnector) handleError(resp *http.Response) error {
	var errResp struct {
		Object  string `json:"object"`
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    "failed to decode error response",
			Provider:   "mistral",
		}
	}


	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    errResp.Message,
		Type:       errResp.Type,
		Code:       errResp.Code,
		Provider:   "mistral",
	}
}


