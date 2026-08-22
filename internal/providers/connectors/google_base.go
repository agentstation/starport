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

	"github.com/agentstation/starport/internal/credentials"
)

// googleBaseConnector provides shared implementation for Google connectors
type googleBaseConnector struct {
	config          ProviderConfig
	httpClient      *http.Client
	name            string
	mapFinishReason func(string) string
}

// Chat performs a chat completion request
func (c *googleBaseConnector) Chat(ctx context.Context, req *ChatRequest, getEndpoint func(*ChatRequest, bool) (string, error), setHeaders func(credentials.Material, *http.Request) error) (*ChatResponse, error) {
	geminiReq := c.convertToGeminiRequest(req)

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := getEndpoint(req, false)
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
		return nil, c.handleError(resp)
	}

	// Parse Gemini response
	var geminiResp geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to OpenAI format
	return c.convertToOpenAIResponse(&geminiResp, req), nil
}

// ChatStream performs a streaming chat completion request
func (c *googleBaseConnector) ChatStream(ctx context.Context, req *ChatRequest, getEndpoint func(*ChatRequest, bool) (string, error), setHeaders func(credentials.Material, *http.Request) error) (ChatStream, error) {
	geminiReq := c.convertToGeminiRequest(req)

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := getEndpoint(req, true)
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
		return nil, c.handleError(resp)
	}

	return newGoogleStream(
		resp,
		req.Model,
		c.mapFinishReason,
		req.Reasoning != nil && req.Reasoning.Exclude,
	), nil
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

// geminiParts converts one message's content into Gemini parts. Text
// parts map to text; data-URL images become inline data; remote image
// URLs pass through as file data so the provider reports its own
// contract error instead of the gateway guessing.
func geminiParts(content MessageContent) []map[string]any {
	parts, err := ParseMessageContent(content)
	if err != nil || len(parts) == 0 {
		return []map[string]any{{contentTypeText: ""}}
	}
	result := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		if part.ImageURL != nil {
			if mediaType, data, ok := parseImageDataURL(part.ImageURL.URL); ok {
				result = append(result, map[string]any{
					"inline_data": map[string]any{
						"mime_type": mediaType,
						"data":      data,
					},
				})
			} else {
				result = append(result, map[string]any{
					"file_data": map[string]any{"file_uri": part.ImageURL.URL},
				})
			}
			continue
		}
		result = append(result, map[string]any{contentTypeText: part.Text})
	}
	return result
}

// convertToGeminiRequest converts OpenAI format to Gemini format
func (c *googleBaseConnector) convertToGeminiRequest(req *ChatRequest) map[string]any {
	var contents []map[string]any
	var systemText string

	for _, msg := range req.Messages {
		var role string
		switch msg.Role {
		case RoleUser:
			role = RoleUser
		case RoleAssistant:
			role = wireModelToken
		case RoleSystem:
			// Gemini doesn't have system role, prepend to first user message
			if text := contentText(msg.Content); text != "" {
				if systemText != "" {
					systemText += "\n\n"
				}
				systemText += text
			}
			continue
		default:
			role = RoleUser
		}

		contents = append(contents, map[string]any{
			"role":  role,
			"parts": geminiParts(msg.Content),
		})
	}

	// Handle system message by prepending to first user message
	if systemText != "" && len(contents) > 0 && contents[0]["role"] == RoleUser {
		parts := contents[0]["parts"].([]map[string]any)
		if existing, ok := parts[0][contentTypeText].(string); ok {
			parts[0][contentTypeText] = systemText + "\n\n" + existing
		} else {
			// The first part is an image; the system text gets its own
			// leading text part.
			contents[0]["parts"] = append(
				[]map[string]any{{contentTypeText: systemText}},
				parts...,
			)
		}
	}

	geminiReq := map[string]any{
		"contents": contents,
	}

	// Generation config
	genConfig := make(map[string]any)
	if req.Temperature != nil {
		genConfig[wireFieldTemperature] = *req.Temperature
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

	// Handle OpenRouter-style reasoning configuration
	if req.Reasoning != nil && !req.Reasoning.Exclude {
		// Create thinkingConfig for Gemini 2.5 models
		thinkingConfig := make(map[string]any)

		// Handle max_tokens if specified (takes precedence over effort)
		if req.Reasoning.MaxTokens != nil {
			thinkingConfig["thinkingBudget"] = *req.Reasoning.MaxTokens
		} else if req.Reasoning.Effort != "" {
			// Handle effort level -> thinking budget mapping
			switch req.Reasoning.Effort {
			case "high":
				thinkingConfig["thinkingBudget"] = -1 // Dynamic thinking
			case "medium":
				thinkingConfig["thinkingBudget"] = 10000
			case "low":
				thinkingConfig["thinkingBudget"] = 5000
			default:
				// Default to dynamic thinking for unknown effort levels
				thinkingConfig["thinkingBudget"] = -1
			}
		} else {
			// Default to dynamic thinking if no effort or max_tokens specified
			thinkingConfig["thinkingBudget"] = -1
		}

		// IMPORTANT: Request thought summaries to be included
		thinkingConfig["includeThoughts"] = true

		genConfig["thinkingConfig"] = thinkingConfig
	}

	if len(genConfig) > 0 {
		geminiReq["generationConfig"] = genConfig
	}

	return geminiReq
}

// convertToOpenAIResponse converts Gemini response to OpenAI format
func (c *googleBaseConnector) convertToOpenAIResponse(resp *geminiResponse, req *ChatRequest) *ChatResponse {
	if len(resp.Candidates) == 0 {
		return &ChatResponse{
			ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
			Object:  objectChatCompletion,
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []Choice{},
		}
	}

	candidate := resp.Candidates[0]
	content := ""
	reasoning := ""

	// Separate thought parts from content parts
	for _, part := range candidate.Content.Parts {
		if part.Thought {
			// This is a raw reasoning/thought part
			if part.Text != "" {
				reasoning += part.Text
			}
		} else {
			// This is regular content - check if it contains thought summaries
			if part.Text != "" {
				// Look for embedded thought summaries
				partContent, partReasoning := extractThoughtSummary(part.Text)
				content += partContent
				if partReasoning != "" {
					if reasoning != "" {
						reasoning += "\n\n"
					}
					reasoning += partReasoning
				}
			}
		}
	}

	message := Message{
		Role:    RoleAssistant,
		Content: content,
	}

	// Only add reasoning if it's not empty and not excluded
	if reasoning != "" && (req.Reasoning == nil || !req.Reasoning.Exclude) {
		message.Reasoning = reasoning
	}

	return &ChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  objectChatCompletion,
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      message,
				FinishReason: c.mapFinishReason(candidate.FinishReason),
			},
		},
		Usage: convertGeminiUsage(resp.UsageMetadata),
	}
}

// convertGeminiUsage normalizes Gemini usage metadata. promptTokenCount
// already includes cachedContentTokenCount, matching OpenAI semantics.
func convertGeminiUsage(m geminiUsageMetadata) Usage {
	usage := Usage{
		PromptTokens:     m.PromptTokenCount,
		CompletionTokens: m.CandidatesTokenCount,
		TotalTokens:      m.TotalTokenCount,
	}
	if m.ThoughtsTokenCount > 0 {
		usage.CompletionTokensDetails = &CompletionTokensDetails{ReasoningTokens: m.ThoughtsTokenCount}
	}
	if m.CachedContentTokenCount > 0 {
		usage.PromptTokensDetails = &PromptTokensDetails{CachedTokens: m.CachedContentTokenCount}
	}
	return usage
}

// googleStream implements ChatStream for Google responses
type googleStream struct {
	response         *http.Response
	reader           *bufio.Reader
	model            string
	mapFinishReason  func(string) string
	closed           bool
	buffer           []byte
	excludeReasoning bool
}

func newGoogleStream(
	resp *http.Response,
	model string,
	mapReason func(string) string,
	excludeReasoning bool,
) *googleStream {
	return &googleStream{
		response:         resp,
		reader:           bufio.NewReader(resp.Body),
		model:            model,
		mapFinishReason:  mapReason,
		buffer:           make([]byte, 0),
		excludeReasoning: excludeReasoning,
	}
}

func (s *googleStream) Recv() (*ChatStreamChunk, error) {
	if s.closed {
		return nil, ErrStreamClosed
	}

	// Google's streaming format is a JSON array of objects separated by commas
	// Format: [{...},\n{...},\n{...}]
	// We need to handle this differently than line-delimited JSON

	// Read until we have a complete JSON object
	for {
		// Read more data if needed
		chunk := make([]byte, 4096)
		n, err := s.reader.Read(chunk)
		if n > 0 {
			s.buffer = append(s.buffer, chunk[:n]...)
		}

		if err == io.EOF && len(s.buffer) == 0 {
			s.closed = true
			return nil, io.EOF
		}

		if err != nil && err != io.EOF {
			return nil, &StreamError{
				Err:    err,
				Reason: streamReadFailureReason,
			}
		}

		// Try to parse a complete JSON object from the buffer
		chunk, remaining, found := s.extractNextChunk()
		if !found {
			if err == io.EOF {
				// No more data and no complete object
				s.closed = true
				return nil, io.EOF
			}
			// Need more data
			continue
		}

		s.buffer = remaining

		// Parse the extracted chunk
		var geminiResp geminiResponse
		if err := json.Unmarshal(chunk, &geminiResp); err != nil {
			// Skip malformed chunks
			continue
		}

		// A rejection inside the stream body must fail the stream, never
		// pass as a candidate-free chunk.
		if geminiResp.Error != nil {
			s.closed = true
			statusCode := geminiResp.Error.Code
			if statusCode < 400 {
				statusCode = http.StatusBadGateway
			}
			return nil, &APIError{
				StatusCode: statusCode,
				Type:       geminiResp.Error.Status,
				Message:    geminiResp.Error.Message,
			}
		}

		// Convert to OpenAI format
		if len(geminiResp.Candidates) > 0 {
			content := ""
			reasoning := ""

			// Separate thought parts from content parts
			for _, part := range geminiResp.Candidates[0].Content.Parts {
				if part.Thought {
					// This is a raw reasoning/thought part
					if part.Text != "" {
						reasoning += part.Text
					}
				} else {
					// Check for embedded thought summaries in content
					if part.Text != "" {
						partContent, partReasoning := extractThoughtSummary(part.Text)
						content += partContent
						if partReasoning != "" {
							reasoning += partReasoning
						}
					}
				}
			}

			finishReason := s.mapFinishReason(geminiResp.Candidates[0].FinishReason)

			// Create delta with content and/or reasoning
			delta := MessageDelta{}
			if content != "" {
				delta.Content = content
			}
			if reasoning != "" && !s.excludeReasoning {
				delta.Reasoning = reasoning
			}

			chunk := &ChatStreamChunk{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  objectChatCompletionChunk,
				Created: time.Now().Unix(),
				Model:   s.model,
				Choices: []StreamChoice{
					{
						Index:        0,
						Delta:        delta,
						FinishReason: finishReason,
					},
				},
			}

			// Include usage metadata if available (typically in the final chunk)
			if geminiResp.UsageMetadata.TotalTokenCount > 0 {
				usage := convertGeminiUsage(geminiResp.UsageMetadata)
				chunk.Usage = &usage
			}

			return chunk, nil
		}
	}
}

// extractNextChunk attempts to extract a complete JSON object from the buffer
func (s *googleStream) extractNextChunk() ([]byte, []byte, bool) {
	// Skip leading whitespace and array brackets
	start := 0
	for start < len(s.buffer) {
		ch := s.buffer[start]
		if ch != ' ' && ch != '\n' && ch != '\r' && ch != '\t' && ch != '[' && ch != ',' {
			break
		}
		start++
	}

	if start >= len(s.buffer) {
		return nil, s.buffer[start:], false
	}

	// Look for a complete JSON object starting with {
	if s.buffer[start] != '{' {
		return nil, s.buffer[start:], false
	}

	// Count braces to find the end of the object
	braceCount := 0
	inString := false
	escaped := false
	end := start

	for end < len(s.buffer) {
		ch := s.buffer[end]

		if !escaped {
			if ch == '"' && !inString {
				inString = true
			} else if ch == '"' && inString {
				inString = false
			} else if ch == '\\' && inString {
				escaped = true
			} else if !inString {
				switch ch {
				case '{':
					braceCount++
				case '}':
					braceCount--
					if braceCount == 0 {
						// Found complete object
						return s.buffer[start : end+1], s.buffer[end+1:], true
					}
				}
			}
		} else {
			escaped = false
		}

		end++
	}

	// Incomplete object, need more data
	return nil, s.buffer[start:], false
}

func (s *googleStream) Close() error {
	s.closed = true
	return s.response.Body.Close()
}

func mapFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return finishReasonStop
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	default:
		return reason
	}
}

// extractThoughtSummary looks for thought summaries in various formats
func extractThoughtSummary(text string) (content, reasoning string) {
	content = text
	reasoning = ""

	// Pattern 1: <thinking>...</thinking> tags
	if start := strings.Index(text, "<thinking>"); start != -1 {
		if end := strings.Index(text[start:], "</thinking>"); end != -1 {
			end += start
			reasoning = strings.TrimSpace(text[start+10 : end])
			content = strings.TrimSpace(text[:start] + text[end+11:])
			return
		}
	}

	// Pattern 2: Thought: prefix at the beginning
	if strings.HasPrefix(text, "Thought:") || strings.HasPrefix(text, "Thinking:") {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				// Found empty line, split here
				reasoning = strings.Join(lines[:i], "\n")
				content = strings.Join(lines[i+1:], "\n")
				return
			}
		}
	}

	// Pattern 3: [Thinking Process] or similar markers
	markers := []string{"[Thinking Process]", "[Thought Process]", "[Internal Reasoning]"}
	for _, marker := range markers {
		if idx := strings.Index(text, marker); idx != -1 {
			// Find the end of the thinking section (next marker or double newline)
			endIdx := strings.Index(text[idx:], "\n\n")
			if endIdx == -1 {
				endIdx = len(text) - idx
			} else {
				endIdx += idx
			}
			reasoning = strings.TrimSpace(text[idx:endIdx])
			content = strings.TrimSpace(text[:idx] + text[endIdx:])
			return
		}
	}

	return content, reasoning
}
