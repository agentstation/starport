package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/agentstation/starmap/pkg/catalogs"
)

const mistralProviderName = string(catalogs.ProviderIDMistralAI)

// MistralConnector implements the Connector interface for Mistral
type MistralConnector struct {
	OpenAICompatibleConnector
}

// NewMistralConnector creates a new Mistral connector
func NewMistralConnector(config ProviderConfig) (*MistralConnector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	httpClient, err := newProviderHTTPClient(mistralProviderName, config)
	if err != nil {
		return nil, err
	}

	return &MistralConnector{
		OpenAICompatibleConnector: OpenAICompatibleConnector{
			config:     config,
			provider:   mistralProviderName,
			httpClient: httpClient,
		},
	}, nil
}

// Name returns the provider name
func (c *MistralConnector) Name() string {
	return mistralProviderName
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

// Close closes the connector
func (c *MistralConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// setHeaders sets Mistral-specific headers
func (c *MistralConnector) setHeaders(req *http.Request) {
	applyRegisteredInferenceAuth(catalogs.ProviderIDMistralAI, req, c.config.APIKey)
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
			Provider:   mistralProviderName,
		}
	}

	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    errResp.Message,
		Type:       errResp.Type,
		Code:       errResp.Code,
		Provider:   mistralProviderName,
	}
}
