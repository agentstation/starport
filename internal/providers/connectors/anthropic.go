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

	"github.com/agentstation/starport/internal/credentials"
)

// AnthropicConnector implements the Connector interface for Anthropic
type AnthropicConnector struct {
	config     ProviderConfig
	httpClient *http.Client
	provider   string
}

// NewAnthropicConnector creates a new Anthropic connector
func NewAnthropicConnector(config ProviderConfig) (*AnthropicConnector, error) {
	return newAnthropicConnector(string(catalogs.ProviderIDAnthropic), config)
}

func newAnthropicConnector(provider string, config ProviderConfig) (*AnthropicConnector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	httpClient := newProviderHTTPClient(config)

	return &AnthropicConnector{
		config:     config,
		httpClient: httpClient,
		provider:   provider,
	}, nil
}

// Name returns the provider name
func (c *AnthropicConnector) Name() string {
	return c.provider
}

// Chat performs a chat completion request
func (c *AnthropicConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeAnthropic)
	if err != nil {
		return nil, err
	}
	request, includeModel := prepareAnthropicRequest(req)
	return executeAnthropicChat(ctx, c.httpClient, endpoint, request, includeModel, c.setHeaders, c.handleError)
}

func executeAnthropicChat(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	req *ChatRequest,
	includeModel bool,
	setHeaders func(credentials.Material, *http.Request) error,
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

	if err := setHeaders(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}

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
	request, includeModel := prepareAnthropicRequest(req)
	return executeAnthropicStream(ctx, c.httpClient, endpoint, request, includeModel, c.setHeaders, c.handleError)
}

func executeAnthropicStream(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	req *ChatRequest,
	includeModel bool,
	setHeaders func(credentials.Material, *http.Request) error,
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

	if err := setHeaders(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}
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
		Provider:   c.provider,
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
func (c *AnthropicConnector) setHeaders(material credentials.Material, req *http.Request) error {
	req.Header.Set("Content-Type", "application/json")
	if err := applyRequestAuthentication(material, req); err != nil {
		return err
	}
	if material.Profile().Primitive != catalogs.ProviderAuthenticationGoogleDefault {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	req.Header.Set("User-Agent", "starport/1.0")
	return nil
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
			Provider:   c.provider,
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	return &APIError{
		Provider:   c.provider,
		StatusCode: resp.StatusCode,
		Type:       errResp.Error.Type,
		Message:    errResp.Error.Message,
	}
}

func prepareAnthropicRequest(req *ChatRequest) (*ChatRequest, bool) {
	if req.Credential.Profile().Primitive != catalogs.ProviderAuthenticationGoogleDefault {
		return req, true
	}
	copyRequest := *req
	copyRequest.ProviderOptions = make(map[string]any, len(req.ProviderOptions)+1)
	for field, value := range req.ProviderOptions {
		copyRequest.ProviderOptions[field] = value
	}
	copyRequest.ProviderOptions["anthropic_version"] = "vertex-2023-10-16"
	return &copyRequest, false
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
			if text := contentText(msg.Content); text != "" {
				system = text
			}
			continue
		}

		anthropicMsg := map[string]any{
			"role": msg.Role,
		}

		// Handle content
		if strContent, ok := msg.Content.(string); ok {
			anthropicMsg["content"] = strContent
		} else if parts, err := ParseMessageContent(msg.Content); err == nil {
			// Handle multimodal content
			var content []map[string]any
			for _, part := range parts {
				if part.ImageURL != nil {
					if mediaType, data, ok := parseImageDataURL(part.ImageURL.URL); ok {
						content = append(content, map[string]any{
							wireTypeToken: "image",
							"source": map[string]any{
								wireTypeToken: "base64",
								"media_type":  mediaType,
								"data":        data,
							},
						})
					} else {
						content = append(content, map[string]any{
							wireTypeToken: "image",
							"source": map[string]any{
								wireTypeToken: "url",
								"url":         part.ImageURL.URL,
							},
						})
					}
					continue
				}
				content = append(content, map[string]any{
					wireTypeToken:   contentTypeText,
					contentTypeText: part.Text,
				})
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
		Usage: convertAnthropicUsage(resp.Usage),
	}
}

// anthropicUsage is the Anthropic wire usage object. Its input_tokens field
// excludes cache reads and writes, unlike OpenAI prompt_tokens.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// convertAnthropicUsage normalizes Anthropic usage to OpenAI semantics:
// prompt_tokens includes cached tokens.
func convertAnthropicUsage(u anthropicUsage) Usage {
	promptTokens := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	usage := Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      promptTokens + u.OutputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
	}
	if u.CacheReadInputTokens != 0 {
		usage.PromptTokensDetails = &PromptTokensDetails{CachedTokens: u.CacheReadInputTokens}
	}
	return usage
}

// anthropicResponse represents the response from Anthropic API
type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// anthropicStream implements ChatStream for Anthropic SSE responses
type anthropicStream struct {
	response *http.Response
	reader   *bufio.Reader
	model    string
	closed   bool

	// Latched from the message_start event. The message_delta usage event
	// reports output tokens only, so prompt-side counts come from here.
	messageID   string
	promptUsage anthropicUsage
	promptKnown bool
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

			// An "error" event is a provider rejection inside the 200
			// stream. It must fail the stream, never convert into an
			// empty chunk.
			if event.Type == sseEventError {
				s.closed = true
				apiErr := &APIError{
					StatusCode: http.StatusBadGateway,
					Message:    string(data),
				}
				if event.Error != nil {
					apiErr.StatusCode = anthropicStreamErrorStatus(event.Error.Type)
					apiErr.Type = event.Error.Type
					apiErr.Message = event.Error.Message
				}
				return nil, apiErr
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
		if event.Message.Usage != nil {
			s.promptUsage = *event.Message.Usage
			s.promptKnown = true
		}

	case "content_block_delta":
		if event.Delta.Type == "text_delta" {
			chunk.Choices[0].Delta.Content = event.Delta.Text
		}

	case "message_delta":
		if event.Delta.StopReason != "" {
			chunk.Choices[0].FinishReason = event.Delta.StopReason
		}
		if event.Usage != nil {
			composed := *event.Usage
			if s.promptKnown {
				composed.InputTokens = s.promptUsage.InputTokens
				composed.CacheCreationInputTokens = s.promptUsage.CacheCreationInputTokens
				composed.CacheReadInputTokens = s.promptUsage.CacheReadInputTokens
			}
			usage := convertAnthropicUsage(composed)
			chunk.Usage = &usage
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
		ID    string          `json:"id"`
		Usage *anthropicUsage `json:"usage,omitempty"`
	} `json:"message,omitempty"`
	Delta *struct {
		Type       string `json:"type,omitempty"`
		Text       string `json:"text,omitempty"`
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`
	Usage *anthropicUsage `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// anthropicStreamErrorStatus recovers the HTTP status Anthropic documents
// for each in-stream error type.
func anthropicStreamErrorStatus(errorType string) int {
	switch errorType {
	case "invalid_request_error":
		return http.StatusBadRequest
	case "authentication_error":
		return http.StatusUnauthorized
	case permissionErrorType:
		return http.StatusForbidden
	case "not_found_error":
		return http.StatusNotFound
	case "rate_limit_error":
		return http.StatusTooManyRequests
	case "overloaded_error":
		return 529
	default:
		return http.StatusBadGateway
	}
}
