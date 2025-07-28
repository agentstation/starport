package connectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentstation/starport/pkg/catalog"
	"github.com/agentstation/starport/pkg/httpclient"
)

// AnthropicConnector implements the Connector interface for Anthropic
type AnthropicConnector struct {
	config     ProviderConfig
	httpClient *http.Client
}

// NewAnthropicConnector creates a new Anthropic connector
func NewAnthropicConnector(config ProviderConfig) (*AnthropicConnector, error) {
	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = "https://api.anthropic.com/v1"
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create HTTP client using httpclient package
	client, err := httpclient.New("anthropic", httpclient.DefaultProviderConfig("anthropic"))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return &AnthropicConnector{
		config:     config,
		httpClient: client.GetHTTPClient(),
	}, nil
}

// Name returns the provider name
func (c *AnthropicConnector) Name() string {
	return "anthropic"
}

// Chat performs a chat completion request
func (c *AnthropicConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	anthropicReq := c.convertToAnthropicRequest(req)
	anthropicReq["stream"] = false

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/messages", bytes.NewReader(body))
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

	// Parse Anthropic response
	var anthropicResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to OpenAI format
	return c.convertToOpenAIResponse(&anthropicResp, req.Model), nil
}

// ChatStream performs a streaming chat completion request
func (c *AnthropicConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	anthropicReq := c.convertToAnthropicRequest(req)
	anthropicReq["stream"] = true

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/messages", bytes.NewReader(body))
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

	return newAnthropicStream(resp, req.Model), nil
}

// Embeddings generates embeddings for the given input
func (c *AnthropicConnector) Embeddings(_ context.Context, _ *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Anthropic doesn't support embeddings directly
	return nil, &APIError{
		Provider:   "anthropic",
		StatusCode: http.StatusNotImplemented,
		Message:    "Anthropic does not support embeddings endpoint",
	}
}

// Models lists available models from the provider
func (c *AnthropicConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	return fetchModelsWithCache(ctx, "anthropic", func(ctx context.Context) (*ModelsResponse, error) {
		// Try to fetch models dynamically from Anthropic API
		req, err := http.NewRequestWithContext(ctx, "GET", c.config.BaseURL+"/models", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Set headers
		req.Header.Set("x-api-key", c.config.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Fall back to catalog models on error
			return c.modelsFromCatalog(), nil
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			// Fall back to catalog models on error
			return c.modelsFromCatalog(), nil
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			// Fall back to catalog models on error
			return c.modelsFromCatalog(), nil
		}

		// Parse the response
		models, err := parseModelsResponse(body, "anthropic")
		if err != nil {
			// Fall back to catalog models on error
			return c.modelsFromCatalog(), nil
		}

		return models, nil
	})
}

// modelsFromCatalog returns models from the catalog as fallback
func (c *AnthropicConnector) modelsFromCatalog() *ModelsResponse {
	// Use the dynamic catalog helper that includes both static and dynamic models
	models := catalog.GetModelsByProviderWithDynamic("anthropic")
	
	// Convert catalog models to connector models
	result := make([]Model, 0, len(models))
	for _, m := range models {
		result = append(result, Model{
			ID:      m.ID,
			Object:  "model",
			Created: m.Created,
			OwnedBy: "anthropic",
		})
	}

	return &ModelsResponse{
		Object: "list",
		Data:   result,
	}
}

// Health checks the health of the connector
func (c *AnthropicConnector) Health(ctx context.Context) error {
	// Simple health check - try to get response with minimal request
	// Use a stable model that's likely to exist
	req := &ChatRequest{
		Model: "anthropic/claude-3-haiku-20240307",
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
		// Check if it's a model not found error
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusNotFound {
			// Try with a basic claude-3 model
			req.Model = "anthropic/claude-3-sonnet-20240229"
			_, err2 := c.Chat(ctx, req)
			if err2 != nil {
				if apiErr2, ok := err2.(*APIError); ok && apiErr2.StatusCode == http.StatusUnauthorized {
					return nil // Service is up, just no valid key
				}
				return err2
			}
			return nil
		}
		return err
	}
	return nil
}

// Close cleans up any resources
func (c *AnthropicConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// setHeaders sets common headers for requests
func (c *AnthropicConnector) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", "starport/1.0")
}

// handleError handles error responses from the API
func (c *AnthropicConnector) handleError(resp *http.Response) error {
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return &APIError{
			Provider:   "anthropic",
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	return &APIError{
		Provider:   "anthropic",
		StatusCode: resp.StatusCode,
		Type:       errResp.Error.Type,
		Message:    errResp.Error.Message,
	}
}

// convertToAnthropicRequest converts OpenAI format to Anthropic format
func (c *AnthropicConnector) convertToAnthropicRequest(req *ChatRequest) map[string]interface{} {
	anthropicReq := make(map[string]interface{})

	// Model - resolve alias and strip provider prefix
	modelID := req.Model
	// Try to resolve alias from catalog
	if cat, err := catalog.GetCatalog(); err == nil {
		modelID = cat.ResolveModelAlias(modelID)
	}
	// Strip provider prefix for Anthropic API
	model := strings.TrimPrefix(modelID, "anthropic/")
	// Anthropic API expects dots to be replaced with hyphens in model names
	// specifically for Claude 3.5 models (e.g., "claude-3.5-sonnet" becomes "claude-3-5-sonnet")
	if strings.Contains(model, "claude-3.5-") {
		model = strings.ReplaceAll(model, ".", "-")
	}
	anthropicReq["model"] = model

	// Convert messages
	var messages []map[string]interface{}
	var system string

	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			// Anthropic uses a separate system field
			if strContent, ok := msg.Content.(string); ok {
				system = strContent
			}
			continue
		}

		anthropicMsg := map[string]interface{}{
			"role": msg.Role,
		}

		// Handle content
		if strContent, ok := msg.Content.(string); ok {
			anthropicMsg["content"] = strContent
		} else if parts, ok := msg.Content.([]ContentPart); ok {
			// Handle multimodal content
			var content []map[string]interface{}
			for _, part := range parts {
				if part.Type == "text" {
					content = append(content, map[string]interface{}{
						"type": "text",
						"text": part.Text,
					})
				} else if part.Type == "image_url" && part.ImageURL != nil {
					content = append(content, map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type":       "base64",
							"media_type": "image/jpeg",      // TODO: detect from URL
							"data":       part.ImageURL.URL, // Assuming base64 data
						},
					})
				}
			}
			anthropicMsg["content"] = content
		}

		messages = append(messages, anthropicMsg)
	}

	anthropicReq["messages"] = messages
	if system != "" {
		anthropicReq["system"] = system
	}

	// Optional parameters
	if req.MaxTokens != nil {
		anthropicReq["max_tokens"] = *req.MaxTokens
	} else {
		anthropicReq["max_tokens"] = 4096 // Anthropic requires this
	}

	if req.Temperature != nil {
		anthropicReq["temperature"] = *req.Temperature
	}

	if req.TopP != nil {
		anthropicReq["top_p"] = *req.TopP
	}

	if len(req.Stop) > 0 {
		anthropicReq["stop_sequences"] = req.Stop
	}

	return anthropicReq
}

// convertToOpenAIResponse converts Anthropic response to OpenAI format
func (c *AnthropicConnector) convertToOpenAIResponse(resp *anthropicResponse, model string) *ChatResponse {
	content := ""
	for _, block := range resp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return &ChatResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: resp.StopReason,
			},
		},
		Usage: Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
}

// anthropicResponse represents the response from Anthropic API
type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// anthropicStream implements ChatStream for Anthropic SSE responses
type anthropicStream struct {
	response  *http.Response
	reader    *bufio.Reader
	model     string
	closed    bool
	messageID string
}

func newAnthropicStream(resp *http.Response, model string) *anthropicStream {
	return &anthropicStream{
		response: resp,
		reader:   bufio.NewReader(resp.Body),
		model:    model,
	}
}

// Recv receives the next chunk of the response
func (s *anthropicStream) Recv() (*ChatStreamChunk, error) {
	if s.closed {
		return nil, ErrStreamClosed
	}

	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				s.closed = true
				return nil, io.EOF
			}
			return nil, &StreamError{
				Err:    err,
				Reason: "failed to read stream",
			}
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// SSE format: "data: {...}" or "event: ..."
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))

			var event anthropicStreamEvent
			if err := json.Unmarshal(data, &event); err != nil {
				return nil, &StreamError{
					Err:    err,
					Reason: "failed to decode chunk",
				}
			}

			// Convert to OpenAI format
			return s.convertToOpenAIChunk(&event), nil
		}
	}
}

// Close closes the stream
func (s *anthropicStream) Close() error {
	s.closed = true
	return s.response.Body.Close()
}

// convertToOpenAIChunk converts Anthropic stream event to OpenAI format
func (s *anthropicStream) convertToOpenAIChunk(event *anthropicStreamEvent) *ChatStreamChunk {
	chunk := &ChatStreamChunk{
		ID:      s.messageID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   s.model,
		Choices: []StreamChoice{
			{
				Index: 0,
				Delta: MessageDelta{},
			},
		},
	}

	switch event.Type {
	case "message_start":
		s.messageID = event.Message.ID
		chunk.ID = s.messageID
		chunk.Choices[0].Delta.Role = "assistant"

	case "content_block_delta":
		if event.Delta.Type == "text_delta" {
			chunk.Choices[0].Delta.Content = event.Delta.Text
		}

	case "message_delta":
		if event.Delta.StopReason != "" {
			chunk.Choices[0].FinishReason = event.Delta.StopReason
		}
		if event.Usage != nil {
			chunk.Usage = &Usage{
				PromptTokens:     event.Usage.InputTokens,
				CompletionTokens: event.Usage.OutputTokens,
				TotalTokens:      event.Usage.InputTokens + event.Usage.OutputTokens,
			}
		}

	case "message_stop":
		// End of stream
		s.closed = true
	}

	return chunk
}

// anthropicStreamEvent represents a streaming event from Anthropic
type anthropicStreamEvent struct {
	Type    string `json:"type"`
	Message *struct {
		ID string `json:"id"`
	} `json:"message,omitempty"`
	Delta *struct {
		Type       string `json:"type,omitempty"`
		Text       string `json:"text,omitempty"`
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// Helper function
func intPtr(i int) *int {
	return &i
}
