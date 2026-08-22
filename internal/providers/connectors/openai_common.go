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

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// OpenAICompatibleConnector provides common implementation for OpenAI-compatible APIs
type OpenAICompatibleConnector struct {
	config     ProviderConfig
	httpClient *http.Client
	provider   string
}

var openAIChatFields = map[string]struct{}{
	wireModelToken: {}, wireFieldMessages: {}, wireFieldTemperature: {}, "top_p": {}, "n": {},
	"max_tokens": {}, wireFieldStream: {}, finishReasonStop: {}, "presence_penalty": {},
	"frequency_penalty": {}, "logit_bias": {}, "user": {}, "seed": {},
	"tools": {}, "tool_choice": {}, "parallel_tool_calls": {}, "response_format": {}, "models": {},
	"stream_options": {}, "reasoning": {}, "provider_options": {},
}

func marshalOpenAIChatRequest(req *ChatRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("chat request is required")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if len(req.ProviderOptions) == 0 {
		return body, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("decode OpenAI request fields: %w", err)
	}
	for name, value := range req.ProviderOptions {
		if _, reserved := openAIChatFields[name]; reserved {
			return nil, fmt.Errorf("provider option %q conflicts with a canonical OpenAI field", name)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode provider option %q: %w", name, err)
		}
		fields[name] = raw
	}
	return json.Marshal(fields)
}

// Chat performs a chat completion request for OpenAI-compatible APIs
func (c *OpenAICompatibleConnector) Chat(ctx context.Context, req *ChatRequest, setHeaders func(credentials.Material, *http.Request) error, handleError func(*http.Response) error) (*ChatResponse, error) {
	// Ensure stream is false for non-streaming request
	req.Stream = false

	body, err := marshalOpenAIChatRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := setHeaders(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}

	resp, err := doRequest(c.httpClient, httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, handleError(resp)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResp, nil
}

// ChatStream performs a streaming chat completion request for OpenAI-compatible APIs
func (c *OpenAICompatibleConnector) ChatStream(ctx context.Context, req *ChatRequest, setHeaders func(credentials.Material, *http.Request) error, handleError func(*http.Response) error, newStream func(*http.Response) ChatStream) (ChatStream, error) {
	// Ensure stream is true for streaming request
	req.Stream = true

	body, err := marshalOpenAIChatRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := setHeaders(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := doRequest(c.httpClient, httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, handleError(resp)
	}

	return newStream(resp), nil
}

// Embeddings generates embeddings for OpenAI-compatible APIs
func (c *OpenAICompatibleConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest, setHeaders func(credentials.Material, *http.Request) error, handleError func(*http.Response) error) (*EmbeddingsResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := setHeaders(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}

	resp, err := doRequest(c.httpClient, httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, handleError(resp)
	}

	var embResp EmbeddingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &embResp, nil
}

// newOpenAICompatibleStream creates a new stream reader
func newOpenAICompatibleStream(resp *http.Response) ChatStream {
	return &openAICompatibleStream{
		response: resp,
		reader:   bufio.NewReader(resp.Body),
		closed:   false,
	}
}

// openAICompatibleStream implements ChatStream for OpenAI-compatible SSE responses
type openAICompatibleStream struct {
	response *http.Response
	reader   *bufio.Reader
	closed   bool
	// event holds the SSE event name for the data lines that follow it.
	// Providers such as Groq deliver rejections as an "event: error" frame
	// inside an established 200 stream.
	event string
}

// Recv reads the next chunk from the stream
func (s *openAICompatibleStream) Recv() (*ChatStreamChunk, error) {
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

		if eventName, ok := bytes.CutPrefix(line, []byte("event:")); ok {
			s.event = string(bytes.TrimSpace(eventName))
			continue
		}

		// SSE format: "data: {...}"
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		data := bytes.TrimPrefix(line, []byte("data: "))
		eventName := s.event
		s.event = ""

		// Check for end of stream
		if string(data) == SSEDone {
			s.closed = true
			return nil, io.EOF
		}

		if apiErr := decodeStreamAPIError(eventName, data); apiErr != nil {
			s.closed = true
			return nil, apiErr
		}

		var chunk ChatStreamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil, &StreamError{
				Err:    err,
				Reason: "failed to decode chunk",
			}
		}

		return &chunk, nil
	}
}

// decodeStreamAPIError detects a provider rejection delivered inside an
// established 200 stream: an "event: error" frame, or a data frame whose
// payload is a top-level error object. Both shapes must end the stream as a
// provider failure, never decode into an empty chunk.
func decodeStreamAPIError(eventName string, data []byte) *APIError {
	var envelope struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Error == nil {
		if eventName == sseEventError {
			return &APIError{
				StatusCode: http.StatusBadGateway,
				Message:    string(data),
			}
		}
		return nil
	}
	return &APIError{
		StatusCode: statusFromStreamErrorCode(envelope.Error.Type, envelope.Error.Code),
		Message:    envelope.Error.Message,
		Type:       envelope.Error.Type,
		Code:       envelope.Error.Code,
	}
}

const sseEventError = "error"

// statusFromStreamErrorCode recovers the HTTP status a provider would have
// used for the same rejection outside a stream. Unrecognized shapes map to
// 502 so normalization treats them as a retryable provider failure.
func statusFromStreamErrorCode(errorType, errorCode string) int {
	marker := strings.ToLower(errorType + " " + errorCode)
	switch {
	case strings.Contains(marker, "rate_limit"):
		return http.StatusTooManyRequests
	case strings.Contains(marker, "quota"):
		return http.StatusTooManyRequests
	case strings.Contains(marker, "api_key"), strings.Contains(marker, "authentication"):
		return http.StatusUnauthorized
	case strings.Contains(marker, "permission"):
		return http.StatusForbidden
	case strings.Contains(marker, "not_found"):
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}

// Close closes the stream
func (s *openAICompatibleStream) Close() error {
	s.closed = true
	return s.response.Body.Close()
}
