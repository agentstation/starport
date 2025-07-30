package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agentstation/starport/pkg/httpclient"
)

// VertexAIConnector implements the Connector interface for Google Vertex AI
type VertexAIConnector struct {
	googleBaseConnector
	projectID         string
	location          string
	fallbackLocations []string
	currentLocation   int
}

// NewVertexAIConnector creates a new Vertex AI connector
func NewVertexAIConnector(config ProviderConfig) (*VertexAIConnector, error) {
	// Extract project ID and location from config
	projectID := ""
	location := "us-central1"
	var fallbackLocations []string

	if pid, ok := config.Extra["project_id"].(string); ok && pid != "" {
		projectID = pid
	} else {
		return nil, fmt.Errorf("project_id is required for Vertex AI")
	}

	if loc, ok := config.Extra["location"].(string); ok && loc != "" {
		location = loc
	}

	// Extract fallback locations
	if fallbacks, ok := config.Extra["fallback_locations"].([]interface{}); ok {
		for _, fb := range fallbacks {
			if fbStr, ok := fb.(string); ok {
				fallbackLocations = append(fallbackLocations, fbStr)
			}
		}
	}

	// If no fallback locations are specified, use a default set based on primary location
	if len(fallbackLocations) == 0 {
		fallbackLocations = getDefaultFallbackLocations(location)
	}

	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s",
			location, projectID, location)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create HTTP client using httpclient package
	client, err := httpclient.New("vertex-ai", httpclient.DefaultProviderConfig("vertex-ai"))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return &VertexAIConnector{
		googleBaseConnector: googleBaseConnector{
			config:     config,
			httpClient: client.GetHTTPClient(),
			name:       GoogleVertexAIProvider,
		},
		projectID:         projectID,
		location:          location,
		fallbackLocations: fallbackLocations,
		currentLocation:   0,
	}, nil
}

// Name returns the provider name
func (c *VertexAIConnector) Name() string {
	return GoogleVertexAIProvider
}

// Chat performs a chat completion request
func (c *VertexAIConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Try current location first
	resp, err := c.googleBaseConnector.Chat(ctx, req, c.getEndpoint, c.setHeaders)
	if err == nil {
		return resp, nil
	}

	// If it's not a retryable error, return immediately
	if !c.isRetryableError(err) {
		return nil, err
	}

	// Try fallback locations
	return c.chatWithFailover(ctx, req)
}

// ChatStream performs a streaming chat completion request
func (c *VertexAIConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	// Try current location first
	stream, err := c.googleBaseConnector.ChatStream(ctx, req, c.getEndpoint, c.setHeaders)
	if err == nil {
		return stream, nil
	}

	// If it's not a retryable error, return immediately
	if !c.isRetryableError(err) {
		return nil, err
	}

	// Try fallback locations
	return c.chatStreamWithFailover(ctx, req)
}

// Embeddings generates embeddings for the given input
func (c *VertexAIConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Convert to Vertex AI embeddings request format
	vertexReq := map[string]interface{}{
		"instances": []map[string]interface{}{
			{
				"content": req.Input.(string),
			},
		},
	}

	body, err := json.Marshal(vertexReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := c.getEmbeddingEndpoint(req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
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
		Object: "list",
		Data: []Embedding{
			{
				Object:    "embedding",
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

// Models lists available models from the provider
func (c *VertexAIConnector) Models(_ context.Context) (*ModelsResponse, error) {
	// Vertex AI doesn't have a models listing endpoint, return static list
	return c.staticModelsList(), nil
}

// staticModelsList returns the hardcoded list of Vertex AI models
func (c *VertexAIConnector) staticModelsList() *ModelsResponse {
	providerPrefix := GoogleVertexAIProvider

	// Vertex AI models include Gemini models plus other Vertex AI exclusive models
	models := []Model{
		// Gemini models on Vertex AI
		{
			ID:      providerPrefix + "/gemini-1.5-pro",
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
		// Claude models via Vertex AI Model Garden
		{
			ID:      providerPrefix + "/claude-3-opus@20240229",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "anthropic",
		},
		{
			ID:      providerPrefix + "/claude-3-sonnet@20240229",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "anthropic",
		},
		{
			ID:      providerPrefix + "/claude-3-haiku@20240307",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "anthropic",
		},
		// PaLM models
		{
			ID:      providerPrefix + "/text-bison@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/text-bison-32k@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/text-unicorn@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Chat models
		{
			ID:      providerPrefix + "/chat-bison@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/chat-bison-32k@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Code models (Codey)
		{
			ID:      providerPrefix + "/code-bison@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/code-bison-32k@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/code-gecko@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Embedding models
		{
			ID:      providerPrefix + "/textembedding-gecko@003",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/textembedding-gecko-multilingual@001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/text-embedding-004",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      providerPrefix + "/text-multilingual-embedding-002",
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
func (c *VertexAIConnector) Health(ctx context.Context) error {
	// Simple health check - try to get response with minimal request
	req := &ChatRequest{
		Model: GoogleVertexAIProvider + "/gemini-1.5-flash",
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
func (c *VertexAIConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Helper methods

func (c *VertexAIConnector) getEndpoint(model string, streaming bool) string {
	action := generateContentAction
	if streaming {
		action = streamGenerateContentAction
	}

	// Strip provider prefix from model name
	model = strings.TrimPrefix(model, GoogleVertexAIProvider+"/")
	model = strings.TrimPrefix(model, "google/") // Support legacy prefix

	// Handle special model names for Claude via Model Garden
	if strings.Contains(model, "claude") {
		// Claude models use a different endpoint pattern
		return fmt.Sprintf("%s/publishers/anthropic/models/%s:%s", c.config.BaseURL, model, action)
	}

	return fmt.Sprintf("%s/publishers/google/models/%s:%s", c.config.BaseURL, model, action)
}

func (c *VertexAIConnector) getEmbeddingEndpoint(model string) string {
	// Strip provider prefix from model name
	model = strings.TrimPrefix(model, GoogleVertexAIProvider+"/")
	model = strings.TrimPrefix(model, "google/") // Support legacy prefix
	return fmt.Sprintf("%s/publishers/google/models/%s:predict", c.config.BaseURL, model)
}

func (c *VertexAIConnector) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("User-Agent", "starport/1.0")
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
			Provider:   GoogleVertexAIProvider,
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	return &APIError{
		Provider:   GoogleVertexAIProvider,
		StatusCode: resp.StatusCode,
		Type:       errResp.Error.Status,
		Message:    errResp.Error.Message,
		Code:       fmt.Sprintf("%d", errResp.Error.Code),
	}
}

// chatWithFailover tries to complete a chat request using fallback locations
func (c *VertexAIConnector) chatWithFailover(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var lastErr error

	// Try each fallback location
	for i, fallbackLocation := range c.fallbackLocations {
		// Update the base URL to use the fallback location
		oldBaseURL := c.config.BaseURL
		c.config.BaseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s",
			fallbackLocation, c.projectID, fallbackLocation)

		resp, err := c.googleBaseConnector.Chat(ctx, req, c.getEndpoint, c.setHeaders)
		if err == nil {
			// Success! Update current location for future requests
			c.currentLocation = i + 1 // +1 because 0 is the primary location
			return resp, nil
		}

		lastErr = err
		c.config.BaseURL = oldBaseURL

		// If it's not a retryable error, stop trying
		if !c.isRetryableError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("all locations failed, last error: %w", lastErr)
}

// chatStreamWithFailover tries to complete a streaming request using fallback locations
func (c *VertexAIConnector) chatStreamWithFailover(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	var lastErr error

	// Try each fallback location
	for i, fallbackLocation := range c.fallbackLocations {
		// Update the base URL to use the fallback location
		oldBaseURL := c.config.BaseURL
		c.config.BaseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s",
			fallbackLocation, c.projectID, fallbackLocation)

		stream, err := c.googleBaseConnector.ChatStream(ctx, req, c.getEndpoint, c.setHeaders)
		if err == nil {
			// Success! Update current location for future requests
			c.currentLocation = i + 1 // +1 because 0 is the primary location
			return stream, nil
		}

		lastErr = err
		c.config.BaseURL = oldBaseURL

		// If it's not a retryable error, stop trying
		if !c.isRetryableError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("all locations failed, last error: %w", lastErr)
}

// isRetryableError determines if an error should trigger fallback to another region
func (c *VertexAIConnector) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check if it's an API error
	apiErr, ok := err.(*APIError)
	if !ok {
		// Network errors are retryable
		return true
	}

	// Retry on server errors and rate limits
	switch apiErr.StatusCode {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

// getDefaultFallbackLocations returns default fallback locations based on the primary location
func getDefaultFallbackLocations(primaryLocation string) []string {
	fallbackMap := map[string][]string{
		// US regions
		"us-central1": {"us-east4", "us-west1", "us-west4"},
		"us-east4":    {"us-central1", "us-west1", "us-west4"},
		"us-west1":    {"us-west4", "us-central1", "us-east4"},
		"us-west4":    {"us-west1", "us-central1", "us-east4"},

		// Europe regions
		"europe-west1":  {"europe-west4", "europe-west2", "europe-north1"},
		"europe-west2":  {"europe-west1", "europe-west4", "europe-north1"},
		"europe-west4":  {"europe-west1", "europe-west2", "europe-north1"},
		"europe-north1": {"europe-west4", "europe-west1", "europe-west2"},

		// Asia regions
		"asia-southeast1": {"asia-northeast1", "asia-east1", "asia-south1"},
		"asia-northeast1": {"asia-southeast1", "asia-east1", "asia-south1"},
		"asia-east1":      {"asia-southeast1", "asia-northeast1", "asia-south1"},
		"asia-south1":     {"asia-southeast1", "asia-northeast1", "asia-east1"},

		// Cross-region fallbacks (if primary region group is down)
		"global": {"us-central1", "europe-west1", "asia-southeast1"},
	}

	if fallbacks, ok := fallbackMap[primaryLocation]; ok {
		return fallbacks
	}

	// Default fallback order if location not in map
	return []string{"us-central1", "europe-west4", "asia-southeast1"}
}
