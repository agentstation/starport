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
	
	// Debug log the request
	fmt.Printf("[Gemini] Request body: %s\n", string(body))

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
	
	// Debug log usage metadata
	fmt.Printf("[Gemini] UsageMetadata: %+v\n", geminiResp.UsageMetadata)

	// Convert to OpenAI format
	return c.convertToOpenAIResponse(&geminiResp, req), nil
}

// ChatStream performs a streaming chat completion request
func (c *googleBaseConnector) ChatStream(ctx context.Context, req *ChatRequest, getEndpoint func(string, bool) string, setHeaders func(*http.Request)) (ChatStream, error) {
	geminiReq := c.convertToGeminiRequest(req)

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	// Debug log the request
	fmt.Printf("[Gemini] Request body: %s\n", string(body))

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

	return newGoogleStream(resp, req.Model, c.name, req.Reasoning != nil && req.Reasoning.Exclude), nil
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

	// Handle OpenRouter-style reasoning configuration
	if req.Reasoning != nil && !req.Reasoning.Exclude {
		// Create thinkingConfig for Gemini 2.5 models
		thinkingConfig := make(map[string]interface{})
		
		// Debug log
		fmt.Printf("[Gemini] Reasoning config: effort=%s, max_tokens=%v\n", req.Reasoning.Effort, req.Reasoning.MaxTokens)
		
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
		fmt.Printf("[Gemini] ThinkingConfig: %+v\n", thinkingConfig)
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
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []Choice{},
		}
	}

	candidate := resp.Candidates[0]
	content := ""
	reasoning := ""
	
	// Separate thought parts from content parts
	for i, part := range candidate.Content.Parts {
		fmt.Printf("[Gemini] Part %d: Text='%.100s...', Thought=%v\n", i, part.Text, part.Thought)
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
		Role:    "assistant",
		Content: content,
	}
	
	// Only add reasoning if it's not empty and not excluded
	if reasoning != "" && (req.Reasoning == nil || !req.Reasoning.Exclude) {
		message.Reasoning = reasoning
		fmt.Printf("[Gemini] Found reasoning text (length=%d)\n", len(reasoning))
	}

	return &ChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      message,
				FinishReason: mapFinishReason(candidate.FinishReason),
			},
		},
		Usage: Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
			CompletionTokensDetails: func() *CompletionTokensDetails {
				if resp.UsageMetadata.ThoughtsTokenCount > 0 {
					return &CompletionTokensDetails{
						ReasoningTokens: resp.UsageMetadata.ThoughtsTokenCount,
					}
				}
				return nil
			}(),
		},
	}
}

// googleStream implements ChatStream for Google responses
type googleStream struct {
	response        *http.Response
	reader          *bufio.Reader
	model           string
	provider        string
	closed          bool
	buffer          []byte
	decoder         *json.Decoder
	started         bool
	excludeReasoning bool
}

func newGoogleStream(resp *http.Response, model, provider string, excludeReasoning bool) *googleStream {
	return &googleStream{
		response:        resp,
		reader:          bufio.NewReader(resp.Body),
		model:           model,
		provider:        provider,
		buffer:          make([]byte, 0),
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
				Reason: "failed to read stream",
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
		
		// Debug log usage metadata in streaming
		if geminiResp.UsageMetadata.TotalTokenCount > 0 {
			fmt.Printf("[Gemini Stream] UsageMetadata: %+v\n", geminiResp.UsageMetadata)
		}

		// Convert to OpenAI format
		if len(geminiResp.Candidates) > 0 {
			content := ""
			reasoning := ""
			
			// Separate thought parts from content parts
			for i, part := range geminiResp.Candidates[0].Content.Parts {
				if i == 0 && len(part.Text) > 0 { // Only log first part to avoid spam
					fmt.Printf("[Gemini Stream] Part %d: Text='%.50s...', Thought=%v\n", i, part.Text, part.Thought)
				}
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

			finishReason := ""
			if s.provider == GoogleVertexAIProvider {
				finishReason = mapVertexFinishReason(geminiResp.Candidates[0].FinishReason)
			} else {
				finishReason = mapFinishReason(geminiResp.Candidates[0].FinishReason)
			}

			// Create delta with content and/or reasoning
			delta := MessageDelta{}
			if content != "" {
				delta.Content = content
			}
			if reasoning != "" && !s.excludeReasoning {
				delta.Reasoning = reasoning
				fmt.Printf("[Gemini Stream] Found reasoning text in chunk (length=%d)\n", len(reasoning))
			}
			
			chunk := &ChatStreamChunk{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
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
				chunk.Usage = &Usage{
					PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
					CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
					TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
					CompletionTokensDetails: func() *CompletionTokensDetails {
						if geminiResp.UsageMetadata.ThoughtsTokenCount > 0 {
							return &CompletionTokensDetails{
								ReasoningTokens: geminiResp.UsageMetadata.ThoughtsTokenCount,
							}
						}
						return nil
					}(),
				}
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
				if ch == '{' {
					braceCount++
				} else if ch == '}' {
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
			reasoning = strings.TrimSpace(text[idx : endIdx])
			content = strings.TrimSpace(text[:idx] + text[endIdx:])
			return
		}
	}
	
	return content, reasoning
}
