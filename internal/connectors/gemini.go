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

// GeminiConnector implements the Connector interface for Google Gemini/Vertex AI
type GeminiConnector struct {
	config     ProviderConfig
	httpClient *http.Client
	isVertexAI bool
	projectID  string
	location   string
}

// NewGeminiConnector creates a new Gemini connector
func NewGeminiConnector(config ProviderConfig) (*GeminiConnector, error) {
	// Determine if this is Vertex AI or Gemini API based on config
	isVertexAI := false
	projectID := ""
	location := "us-central1"

	if pid, ok := config.Extra["project_id"].(string); ok && pid != "" {
		isVertexAI = true
		projectID = pid
	}
	if loc, ok := config.Extra["location"].(string); ok && loc != "" {
		location = loc
	}

	// Set default base URL based on API type
	if config.BaseURL == "" {
		if isVertexAI {
			config.BaseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s",
				location, projectID, location)
		} else {
			config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
		}
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        config.MaxConnections,
		MaxIdleConnsPerHost: config.MaxConnections,
		IdleConnTimeout:     90 * time.Second,
	}

	return &GeminiConnector{
		config: config,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   config.Timeout,
		},
		isVertexAI: isVertexAI,
		projectID:  projectID,
		location:   location,
	}, nil
}

// Name returns the provider name
func (c *GeminiConnector) Name() string {
	return "gemini"
}

// Chat performs a chat completion request
func (c *GeminiConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	geminiReq := c.convertToGeminiRequest(req)

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := c.getEndpoint(req.Model, false)
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

	// Parse Gemini response
	var geminiResp geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to OpenAI format
	return c.convertToOpenAIResponse(&geminiResp, req.Model), nil
}

// ChatStream performs a streaming chat completion request
func (c *GeminiConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	geminiReq := c.convertToGeminiRequest(req)

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := c.getEndpoint(req.Model, true)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
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

	return newGeminiStream(resp, req.Model), nil
}

// Embeddings generates embeddings for the given input
func (c *GeminiConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Convert to Gemini embeddings request
	geminiReq := map[string]interface{}{
		"content": map[string]interface{}{
			"parts": []map[string]string{
				{"text": req.Input.(string)},
			},
		},
	}

	body, err := json.Marshal(geminiReq)
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

	var geminiResp struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &EmbeddingsResponse{
		Object: "list",
		Data: []Embedding{
			{
				Object:    "embedding",
				Index:     0,
				Embedding: geminiResp.Embedding.Values,
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
func (c *GeminiConnector) Models(_ context.Context) (*ModelsResponse, error) {
	// Hardcoded list as Gemini doesn't have a models endpoint
	models := []Model{
		// Gemini 2.5 models (stable)
		{
			ID:      "google/gemini-2.5-pro",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google/gemini-2.5-flash",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Gemini 2.0 models
		{
			ID:      "google/gemini-2.0-flash-lite",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Gemini 1.5 models
		{
			ID:      "google/gemini-1.5-pro-002",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google/gemini-1.5-pro",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google/gemini-1.5-flash-002",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google/gemini-1.5-flash",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google/gemini-1.5-flash-8b-001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Legacy models
		{
			ID:      "google/gemini-pro",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google/gemini-pro-vision",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Embedding models
		{
			ID:      "google/text-embedding-004",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google/embedding-001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
	}

	return &ModelsResponse{
		Object: "list",
		Data:   models,
	}, nil
}

// Health checks the health of the connector
func (c *GeminiConnector) Health(ctx context.Context) error {
	// Simple health check - try to get response with minimal request
	req := &ChatRequest{
		Model: "gemini-1.5-flash",
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
func (c *GeminiConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Helper methods

func (c *GeminiConnector) getEndpoint(model string, streaming bool) string {
	action := "generateContent"
	if streaming {
		action = "streamGenerateContent"
	}

	if c.isVertexAI {
		return fmt.Sprintf("%s/publishers/google/models/%s:%s", c.config.BaseURL, model, action)
	}
	return fmt.Sprintf("%s/models/%s:%s", c.config.BaseURL, model, action)
}

func (c *GeminiConnector) getEmbeddingEndpoint(model string) string {
	if c.isVertexAI {
		return fmt.Sprintf("%s/publishers/google/models/%s:predict", c.config.BaseURL, model)
	}
	return fmt.Sprintf("%s/models/%s:embedContent", c.config.BaseURL, model)
}

func (c *GeminiConnector) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.isVertexAI {
		// Vertex AI uses OAuth2 token
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	} else {
		// Gemini API uses API key in query param
		q := req.URL.Query()
		q.Set("key", c.config.APIKey)
		req.URL.RawQuery = q.Encode()
	}
	req.Header.Set("User-Agent", "starport/1.0")
}

func (c *GeminiConnector) handleError(resp *http.Response) error {
	var errResp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return &APIError{
			Provider:   "gemini",
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	return &APIError{
		Provider:   "gemini",
		StatusCode: resp.StatusCode,
		Type:       errResp.Error.Status,
		Message:    errResp.Error.Message,
		Code:       fmt.Sprintf("%d", errResp.Error.Code),
	}
}

// convertToGeminiRequest converts OpenAI format to Gemini format
func (c *GeminiConnector) convertToGeminiRequest(req *ChatRequest) map[string]interface{} {
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
func (c *GeminiConnector) convertToOpenAIResponse(resp *geminiResponse, model string) *ChatResponse {
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

// Gemini response types
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []map[string]interface{} `json:"parts"`
			Role  string                   `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
		Index        int    `json:"index"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

// geminiStream implements ChatStream for Gemini responses
type geminiStream struct {
	response *http.Response
	reader   *bufio.Reader
	model    string
	closed   bool
}

func newGeminiStream(resp *http.Response, model string) *geminiStream {
	return &geminiStream{
		response: resp,
		reader:   bufio.NewReader(resp.Body),
		model:    model,
	}
}

func (s *geminiStream) Recv() (*ChatStreamChunk, error) {
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

		// Parse Gemini streaming response
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
						FinishReason: mapFinishReason(chunk.Candidates[0].FinishReason),
					},
				},
			}, nil
		}
	}
}

func (s *geminiStream) Close() error {
	s.closed = true
	return s.response.Body.Close()
}