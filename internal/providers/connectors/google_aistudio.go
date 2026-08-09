package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// GoogleAIStudioConnector implements the Connector interface for Google AI Studio (Gemini)
type GoogleAIStudioConnector struct {
	googleBaseConnector
}

// NewGoogleAIStudioConnector creates a new Google AI Studio connector
func NewGoogleAIStudioConnector(config ProviderConfig) (*GoogleAIStudioConnector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	httpClient, err := newProviderHTTPClient(GoogleAIStudioProvider, config)
	if err != nil {
		return nil, err
	}

	return &GoogleAIStudioConnector{
		googleBaseConnector: googleBaseConnector{
			config:     config,
			httpClient: httpClient,
			name:       GoogleAIStudioProvider,
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
func (c *GoogleAIStudioConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	endpoint, err := selectedEndpoint(
		req.Endpoint,
		catalogs.EndpointTypeGoogle,
	)
	if err != nil {
		return nil, err
	}
	inputs, err := embeddingInputs(req.Input)
	if err != nil {
		return nil, err
	}

	result := &EmbeddingsResponse{Object: objectList, Model: req.Model}
	for index, input := range inputs {
		payload := map[string]any{
			"content": map[string]any{"parts": []map[string]string{{contentTypeText: input}}},
		}
		if req.Dimensions != nil {
			payload["outputDimensionality"] = *req.Dimensions
		}
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal Google embedding request: %w", marshalErr)
		}
		httpReq, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if requestErr != nil {
			return nil, fmt.Errorf("create Google embedding request: %w", requestErr)
		}
		c.setHeaders(httpReq)
		resp, requestErr := doRequest(c.httpClient, httpReq)
		if requestErr != nil {
			return nil, requestErr
		}
		if resp.StatusCode != http.StatusOK {
			providerErr := c.handleError(resp)
			_ = resp.Body.Close()
			return nil, providerErr
		}
		var providerResponse struct {
			Embedding struct {
				Values []float32 `json:"values"`
			} `json:"embedding"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&providerResponse)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode Google embedding response: %w", decodeErr)
		}
		result.Data = append(result.Data, Embedding{
			Object: objectEmbedding, Index: index, Embedding: providerResponse.Embedding.Values,
		})
	}
	return result, nil
}

// Close cleans up any resources
func (c *GoogleAIStudioConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Helper methods

func (c *GoogleAIStudioConnector) getEndpoint(req *ChatRequest, _ bool) (string, error) {
	return selectedEndpoint(req.Endpoint, catalogs.EndpointTypeGoogle)
}

func (c *GoogleAIStudioConnector) setHeaders(req *http.Request) {
	applyRegisteredInferenceAuth(catalogs.ProviderIDGoogleAIStudio, req, c.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "starport/1.0")
}

func embeddingInputs(input any) ([]string, error) {
	switch value := input.(type) {
	case string:
		return []string{value}, nil
	case []string:
		return append([]string(nil), value...), nil
	case []any:
		result := make([]string, len(value))
		for index, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("google embeddings input %d must be text", index)
			}
			result[index] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("google embeddings input must be text or a text array")
	}
}
