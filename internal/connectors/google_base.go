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
)

// Constants for Google operations
const (
	generateContentAction       = "generateContent"
	streamGenerateContentAction = "streamGenerateContent"
)

// googleBaseConnector provides shared implementation for Google connectors
type googleBaseConnector struct {
	config     ProviderConfig
	httpClient *http.Client
	name       string
}

// Chat performs a chat completion request
func (c *googleBaseConnector) Chat(ctx context.Context, req *ChatRequest, getEndpoint func(string, bool) string, setHeaders func(*http.Request)) (*ChatResponse, error) {
	geminiReq := c.convertToGeminiRequest(req)

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := getEndpoint(req.Model, false)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	setHeaders(httpReq)

	resp, err := doRequestWithRetry(c.httpClient, httpReq, c.config)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	// Parse Gemini response
	var geminiResp geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to OpenAI format
	return c.convertToOpenAIResponse(&geminiResp, req.Model), nil
}

// ChatStream performs a streaming chat completion request
func (c *googleBaseConnector) ChatStream(ctx context.Context, req *ChatRequest, getEndpoint func(string, bool) string, setHeaders func(*http.Request)) (ChatStream, error) {
	geminiReq := c.convertToGeminiRequest(req)

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := getEndpoint(req.Model, true)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := doRequestWithRetry(c.httpClient, httpReq, c.config)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, c.handleError(resp)
	}

	return newGoogleStream(resp, req.Model, c.name), nil
}

// handleError handles error responses from Google APIs
func (c *googleBaseConnector) handleError(resp *http.Response) error {
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

// convertToGeminiRequest converts OpenAI format to Gemini format
func (c *googleBaseConnector) convertToGeminiRequest(req *ChatRequest) map[string]interface{} {
	var contents []map[string]interface{}
	
	for _, msg := range req.Messages {
		var role string
		switch msg.Role {
		case RoleUser:
			role = RoleUser
		case RoleAssistant:
			role = "model"
		case RoleSystem:
			// Gemini doesn't have system role, prepend to first user message
			continue
		default:
			role = RoleUser
		}

		content := map[string]interface{}{
			"role": role,
			"parts": []map[string]interface{}{
				{"text": msg.Content.(string)},
			},
		}
		contents = append(contents, content)
	}

	// Handle system message by prepending to first user message
	for i, msg := range req.Messages {
		if msg.Role == "system" && i+1 < len(req.Messages) {
			systemText := msg.Content.(string)
			if len(contents) > 0 && contents[0]["role"] == "user" {
				parts := contents[0]["parts"].([]map[string]interface{})
				parts[0]["text"] = systemText + "\n\n" + parts[0]["text"].(string)
			}
			break
		}
	}

	geminiReq := map[string]interface{}{
		"contents": contents,
	}

	// Generation config
	genConfig := make(map[string]interface{})
	if req.Temperature != nil {
		genConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		genConfig["topP"] = *req.TopP
	}
	if req.MaxTokens != nil {
		genConfig["maxOutputTokens"] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		genConfig["stopSequences"] = req.Stop
	}

	if len(genConfig) > 0 {
		geminiReq["generationConfig"] = genConfig
	}

	return geminiReq
}

// convertToOpenAIResponse converts Gemini response to OpenAI format
func (c *googleBaseConnector) convertToOpenAIResponse(resp *geminiResponse, model string) *ChatResponse {
	if len(resp.Candidates) == 0 {
		return &ChatResponse{
			ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []Choice{},
		}
	}

	candidate := resp.Candidates[0]
	content := ""
	for _, part := range candidate.Content.Parts {
		if text, ok := part["text"].(string); ok {
			content += text
		}
	}

	return &ChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
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
				FinishReason: mapFinishReason(candidate.FinishReason),
			},
		},
		Usage: Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		},
	}
}

// googleStream implements ChatStream for Google responses
type googleStream struct {
	response *http.Response
	reader   *bufio.Reader
	model    string
	provider string
	closed   bool
}

func newGoogleStream(resp *http.Response, model, provider string) *googleStream {
	return &googleStream{
		response: resp,
		reader:   bufio.NewReader(resp.Body),
		model:    model,
		provider: provider,
	}
}

func (s *googleStream) Recv() (*ChatStreamChunk, error) {
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

		// Parse Google streaming response
		var chunk geminiResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue // Skip malformed chunks
		}

		// Convert to OpenAI format
		if len(chunk.Candidates) > 0 {
			content := ""
			for _, part := range chunk.Candidates[0].Content.Parts {
				if text, ok := part["text"].(string); ok {
					content += text
				}
			}

			finishReason := ""
			if s.provider == GoogleVertexAIProvider {
				finishReason = mapVertexFinishReason(chunk.Candidates[0].FinishReason)
			} else {
				finishReason = mapFinishReason(chunk.Candidates[0].FinishReason)
			}

			return &ChatStreamChunk{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   s.model,
				Choices: []StreamChoice{
					{
						Index: 0,
						Delta: MessageDelta{
							Content: content,
						},
						FinishReason: finishReason,
					},
				},
			}, nil
		}
	}
}

func (s *googleStream) Close() error {
	s.closed = true
	return s.response.Body.Close()
}

func mapFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	default:
		return reason
	}
}

func mapVertexFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	default:
		return reason
	}
}