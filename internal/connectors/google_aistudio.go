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

// GoogleAIStudioConnector implements the Connector interface for Google AI Studio (Gemini)
type GoogleAIStudioConnector struct {
	config     ProviderConfig
	httpClient *http.Client
}

// NewGoogleAIStudioConnector creates a new Google AI Studio connector
func NewGoogleAIStudioConnector(config ProviderConfig) (*GoogleAIStudioConnector, error) {
	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
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

	return &GoogleAIStudioConnector{
		config: config,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   config.Timeout,
		},
	}, nil
}

// Name returns the provider name
func (c *GoogleAIStudioConnector) Name() string {
	return "google-aistudio"
}

// Chat performs a chat completion request
func (c *GoogleAIStudioConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
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
func (c *GoogleAIStudioConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
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
func (c *GoogleAIStudioConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
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
func (c *GoogleAIStudioConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	providerPrefix := "google-aistudio"

	// Try dynamic fetching with cache
	return fetchModelsWithCache(ctx, providerPrefix, func(ctx context.Context) (*ModelsResponse, error) {
		// Try to fetch models dynamically from Gemini API
		url := fmt.Sprintf("%s/models?key=%s", c.config.BaseURL, c.config.APIKey)
		
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		// Parse Gemini models response
		var geminiResp struct {
			Models []struct {
				Name            string   `json:"name"`
				DisplayName     string   `json:"displayName"`
				Description     string   `json:"description"`
				SupportedMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
		}
		
		if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
			// Fall back to static list on error
			return c.staticModelsList(), nil
		}

		// Convert to standard format
		models := make([]Model, 0, len(geminiResp.Models))
		for _, m := range geminiResp.Models {
			// Extract model ID from name (e.g., "models/gemini-pro" -> "gemini-pro")
			modelID := strings.TrimPrefix(m.Name, "models/")
			
			// Only include models that support content generation
			supportsGeneration := false
			for _, method := range m.SupportedMethods {
				if method == "generateContent" || method == "streamGenerateContent" {
					supportsGeneration = true
					break
				}
			}
			
			if supportsGeneration {
				models = append(models, Model{
					ID:      providerPrefix + "/" + modelID,
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "google",
				})
			}
		}

		return &ModelsResponse{
			Object: "list",
			Data:   models,
		}, nil
	})
}

// staticModelsList returns the hardcoded list of AI Studio models
func (c *GoogleAIStudioConnector) staticModelsList() *ModelsResponse {
	// AI Studio models
	models := []Model{
		// Gemini 2.5 models (stable)
		{
			ID:      "google-aistudio/gemini-2.5-pro",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google-aistudio/gemini-2.5-flash",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Gemini 2.0 models
		{
			ID:      "google-aistudio/gemini-2.0-flash-lite",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Gemini 1.5 models
		{
			ID:      "google-aistudio/gemini-1.5-pro-002",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google-aistudio/gemini-1.5-flash-002",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Legacy models
		{
			ID:      "google-aistudio/gemini-pro",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		// Embedding models
		{
			ID:      "google-aistudio/embedding-001",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "google",
		},
		{
			ID:      "google-aistudio/text-embedding-004",
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
func (c *GoogleAIStudioConnector) Health(ctx context.Context) error {
	// Simple health check - try to get response with minimal request
	req := &ChatRequest{
		Model: "google-aistudio/gemini-1.5-flash",
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
func (c *GoogleAIStudioConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Helper methods

func (c *GoogleAIStudioConnector) getEndpoint(model string, streaming bool) string {
	action := "generateContent"
	if streaming {
		action = "streamGenerateContent"
	}

	// Strip provider prefix from model name
	model = strings.TrimPrefix(model, "google-aistudio/")
	model = strings.TrimPrefix(model, "google/") // Support legacy prefix
	return fmt.Sprintf("%s/models/%s:%s?key=%s", c.config.BaseURL, model, action, c.config.APIKey)
}

func (c *GoogleAIStudioConnector) getEmbeddingEndpoint(model string) string {
	// Strip provider prefix from model name
	model = strings.TrimPrefix(model, "google-aistudio/")
	model = strings.TrimPrefix(model, "google/") // Support legacy prefix
	return fmt.Sprintf("%s/models/%s:embedContent?key=%s", c.config.BaseURL, model, c.config.APIKey)
}

func (c *GoogleAIStudioConnector) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "starport/1.0")
}

func (c *GoogleAIStudioConnector) handleError(resp *http.Response) error {
	var errResp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return &APIError{
			Provider:   "google-aistudio",
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	return &APIError{
		Provider:   "google-aistudio",
		StatusCode: resp.StatusCode,
		Type:       errResp.Error.Status,
		Message:    errResp.Error.Message,
		Code:       fmt.Sprintf("%d", errResp.Error.Code),
	}
}

// convertToGeminiRequest converts OpenAI format to Gemini format
func (c *GoogleAIStudioConnector) convertToGeminiRequest(req *ChatRequest) map[string]interface{} {
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
func (c *GoogleAIStudioConnector) convertToOpenAIResponse(resp *geminiResponse, model string) *ChatResponse {
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