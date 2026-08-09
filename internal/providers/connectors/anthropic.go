package connectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

const anthropicProviderName = string(catalogs.ProviderIDAnthropic)

// AnthropicConnector implements the Connector interface for Anthropic
type AnthropicConnector struct {
	config     ProviderConfig
	httpClient *http.Client
}

// NewAnthropicConnector creates a new Anthropic connector
func NewAnthropicConnector(config ProviderConfig) (*AnthropicConnector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	httpClient, err := newProviderHTTPClient(anthropicProviderName, config)
	if err != nil {
		return nil, err
	}

	return &AnthropicConnector{
		config:     config,
		httpClient: httpClient,
	}, nil
}

// Name returns the provider name
func (c *AnthropicConnector) Name() string {
	return anthropicProviderName
}

// Chat performs a chat completion request
func (c *AnthropicConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeAnthropic)
	if err != nil {
		return nil, err
	}
	return executeAnthropicChat(ctx, c.httpClient, endpoint, req, true, c.setHeaders, c.handleError)
}

func executeAnthropicChat(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	req *ChatRequest,
	includeModel bool,
	setHeaders func(*http.Request),
	handleError func(*http.Response) error,
) (*ChatResponse, error) {
	anthropicReq := convertToAnthropicRequest(req, includeModel)
	anthropicReq[wireFieldStream] = false

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	setHeaders(httpReq)

	resp, err := doRequest(client, httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, handleError(resp)
	}

	// Parse Anthropic response
	var anthropicResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to OpenAI format
	return convertAnthropicResponse(&anthropicResp, req.Model), nil
}

// ChatStream performs a streaming chat completion request
func (c *AnthropicConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeAnthropic)
	if err != nil {
		return nil, err
	}
	return executeAnthropicStream(ctx, c.httpClient, endpoint, req, true, c.setHeaders, c.handleError)
}

func executeAnthropicStream(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	req *ChatRequest,
	includeModel bool,
	setHeaders func(*http.Request),
	handleError func(*http.Response) error,
) (ChatStream, error) {
	anthropicReq := convertToAnthropicRequest(req, includeModel)
	anthropicReq[wireFieldStream] = true

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := doRequest(client, httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, handleError(resp)
	}

	return newAnthropicStream(resp, req.Model), nil
}

// Embeddings generates embeddings for the given input
func (c *AnthropicConnector) Embeddings(_ context.Context, _ *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Anthropic doesn't support embeddings directly
	return nil, &APIError{
		Provider:   anthropicProviderName,
		StatusCode: http.StatusNotImplemented,
		Message:    "Anthropic does not support embeddings endpoint",
	}
}

// Close cleans up any resources
func (c *AnthropicConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// setHeaders sets common headers for requests
func (c *AnthropicConnector) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	applyRegisteredInferenceAuth(catalogs.ProviderIDAnthropic, req, c.config.APIKey)
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
			Provider:   anthropicProviderName,
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	return &APIError{
		Provider:   anthropicProviderName,
		StatusCode: resp.StatusCode,
		Type:       errResp.Error.Type,
		Message:    errResp.Error.Message,
	}
}

// convertToAnthropicRequest converts OpenAI format to Anthropic format
func convertToAnthropicRequest(req *ChatRequest, includeModel bool) map[string]any {
	anthropicReq := make(map[string]any)

	if includeModel {
		anthropicReq[wireModelToken] = req.Model
	}

	// Convert messages
	var messages []map[string]any
	var system string

	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			// Anthropic uses a separate system field
			if strContent, ok := msg.Content.(string); ok {
				system = strContent
			}
			continue
		}

		anthropicMsg := map[string]any{
			"role": msg.Role,
		}

		// Handle content
		if strContent, ok := msg.Content.(string); ok {
			anthropicMsg["content"] = strContent
		} else if parts, ok := msg.Content.([]ContentPart); ok {
			// Handle multimodal content
			var content []map[string]any
			for _, part := range parts {
				if part.Type == contentTypeText {
					content = append(content, map[string]any{
						wireTypeToken:   contentTypeText,
						contentTypeText: part.Text,
					})
				} else if part.Type == "image_url" && part.ImageURL != nil {
					content = append(content, map[string]any{
						wireTypeToken: "image",
						"source": map[string]any{
							wireTypeToken: "base64",
							"media_type":  "image/jpeg",      // TODO: detect from URL
							"data":        part.ImageURL.URL, // Assuming base64 data
						},
					})
				}
			}
			anthropicMsg["content"] = content
		}

		messages = append(messages, anthropicMsg)
	}

	anthropicReq[wireFieldMessages] = messages
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
		anthropicReq[wireFieldTemperature] = *req.Temperature
	}

	if req.TopP != nil {
		anthropicReq["top_p"] = *req.TopP
	}

	if len(req.Stop) > 0 {
		anthropicReq["stop_sequences"] = req.Stop
	}
	for field, value := range req.ProviderOptions {
		anthropicReq[field] = value
	}

	return anthropicReq
}

// convertToOpenAIResponse converts Anthropic response to OpenAI format
func convertAnthropicResponse(resp *anthropicResponse, model string) *ChatResponse {
	content := ""
	for _, block := range resp.Content {
		if block.Type == contentTypeText {
			content += block.Text
		}
	}

	return &ChatResponse{
		ID:      resp.ID,
		Object:  objectChatCompletion,
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    RoleAssistant,
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
				Reason: streamReadFailureReason,
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
		Object:  objectChatCompletionChunk,
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
		chunk.Choices[0].Delta.Role = RoleAssistant

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
