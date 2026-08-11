package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// VertexAIConnector implements the Connector interface for Google Vertex AI
type VertexAIConnector struct {
	googleBaseConnector
}

// NewVertexAIConnector creates a new Vertex AI connector
func NewVertexAIConnector(config ProviderConfig) (*VertexAIConnector, error) {
	return newGoogleCloudConnector(string(catalogs.ProviderIDGoogleVertex), config)
}

func newGoogleCloudConnector(provider string, config ProviderConfig) (*VertexAIConnector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	httpClient, err := newProviderHTTPClient(provider, config)
	if err != nil {
		return nil, err
	}

	return &VertexAIConnector{
		googleBaseConnector: googleBaseConnector{
			config:          config,
			httpClient:      httpClient,
			name:            provider,
			mapFinishReason: mapFinishReason,
		},
	}, nil
}

// Name returns the provider name
func (c *VertexAIConnector) Name() string {
	return c.name
}

// Chat performs a chat completion request
func (c *VertexAIConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if req.Endpoint.Type == catalogs.EndpointTypeAnthropic {
		endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeAnthropic)
		if err != nil {
			return nil, err
		}
		vertexReq := withVertexAnthropicVersion(req)
		return executeAnthropicChat(ctx, c.httpClient, endpoint, vertexReq, false, c.setHeaders, c.handleError)
	}
	return c.googleBaseConnector.Chat(ctx, req, c.getEndpoint, c.setHeaders)
}

// ChatStream performs a streaming chat completion request
func (c *VertexAIConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	if req.Endpoint.Type == catalogs.EndpointTypeAnthropic {
		endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeAnthropic)
		if err != nil {
			return nil, err
		}
		vertexReq := withVertexAnthropicVersion(req)
		return executeAnthropicStream(ctx, c.httpClient, endpoint, vertexReq, false, c.setHeaders, c.handleError)
	}
	return c.googleBaseConnector.ChatStream(ctx, req, c.getEndpoint, c.setHeaders)
}

// Embeddings generates embeddings for the given input
func (c *VertexAIConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Convert to Vertex AI embeddings request format
	vertexReq := map[string]any{
		"instances": []map[string]any{
			{
				"content": req.Input.(string),
			},
		},
	}

	body, err := json.Marshal(vertexReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeGoogleCloud)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := c.setHeaders(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}

	resp, err := doRequest(c.httpClient, httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var vertexResp struct {
		Predictions []struct {
			Embeddings struct {
				Values []float32 `json:"values"`
			} `json:"embeddings"`
		} `json:"predictions"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&vertexResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(vertexResp.Predictions) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return &EmbeddingsResponse{
		Object: objectList,
		Data: []Embedding{
			{
				Object:    objectEmbedding,
				Index:     0,
				Embedding: vertexResp.Predictions[0].Embeddings.Values,
			},
		},
		Model: req.Model,
		Usage: Usage{
			PromptTokens: len(strings.Fields(req.Input.(string))),
			TotalTokens:  len(strings.Fields(req.Input.(string))),
		},
	}, nil
}

// Close cleans up any resources
func (c *VertexAIConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Helper methods

func (c *VertexAIConnector) getEndpoint(req *ChatRequest, _ bool) (string, error) {
	return selectedEndpoint(req.Endpoint, catalogs.EndpointTypeGoogleCloud)
}

func withVertexAnthropicVersion(req *ChatRequest) *ChatRequest {
	copyRequest := *req
	copyRequest.ProviderOptions = make(map[string]any, len(req.ProviderOptions)+1)
	for field, value := range req.ProviderOptions {
		copyRequest.ProviderOptions[field] = value
	}
	copyRequest.ProviderOptions["anthropic_version"] = "vertex-2023-10-16"
	return &copyRequest
}

func (c *VertexAIConnector) setHeaders(material credentials.Material, req *http.Request) error {
	req.Header.Set("Content-Type", "application/json")
	if err := applyRequestAuthentication(material, req); err != nil {
		return err
	}
	req.Header.Set("User-Agent", "starport/1.0")
	return nil
}

func (c *VertexAIConnector) handleError(resp *http.Response) error {
	var errResp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return &APIError{
			Provider:   c.name,
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	return &APIError{
		Provider:   c.name,
		StatusCode: resp.StatusCode,
		Type:       errResp.Error.Status,
		Message:    errResp.Error.Message,
		Code:       fmt.Sprintf("%d", errResp.Error.Code),
	}
}
