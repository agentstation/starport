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

// VertexAIConnector implements the Connector interface for Google Vertex AI
type VertexAIConnector struct {
	config     ProviderConfig
	httpClient *http.Client
	projectID  string
	location   string
}

// NewVertexAIConnector creates a new Vertex AI connector
func NewVertexAIConnector(config ProviderConfig) (*VertexAIConnector, error) {
	// Extract project ID and location from config
	projectID := ""
	location := "us-central1"
	
	if pid, ok := config.Extra["project_id"].(string); ok && pid != "" {
		projectID = pid
	} else {
		return nil, fmt.Errorf("project_id is required for Vertex AI")
	}
	
	if loc, ok := config.Extra["location"].(string); ok && loc != "" {
		location = loc
	}

	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s",
			location, projectID, location)
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

	return &VertexAIConnector{
		config: config,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   config.Timeout,
		},
		projectID: projectID,
		location:  location,
	}, nil
}

// Name returns the provider name
func (c *VertexAIConnector) Name() string {
	return "google-vertexai"
}

// Chat performs a chat completion request
func (c *VertexAIConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
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
func (c *VertexAIConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
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

	return newVertexStream(resp, req.Model), nil
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
func (c *VertexAIConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	// Vertex AI doesn't have a models listing endpoint, return static list
	return c.staticModelsList(), nil
}

// staticModelsList returns the hardcoded list of Vertex AI models
func (c *VertexAIConnector) staticModelsList() *ModelsResponse {
	providerPrefix := "google-vertexai"
	
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
		Model: "google-vertexai/gemini-1.5-flash",
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
	action := "generateContent"
	if streaming {
		action = "streamGenerateContent"
	}

	// Strip provider prefix from model name
	model = strings.TrimPrefix(model, "google-vertexai/")
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
	model = strings.TrimPrefix(model, "google-vertexai/")
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
			Provider:   "google-vertexai",
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	return &APIError{
		Provider:   "google-vertexai",
		StatusCode: resp.StatusCode,
		Type:       errResp.Error.Status,
		Message:    errResp.Error.Message,
		Code:       fmt.Sprintf("%d", errResp.Error.Code),
	}
}

// convertToGeminiRequest converts OpenAI format to Gemini format
func (c *VertexAIConnector) convertToGeminiRequest(req *ChatRequest) map[string]interface{} {
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
func (c *VertexAIConnector) convertToOpenAIResponse(resp *geminiResponse, model string) *ChatResponse {
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
				FinishReason: mapVertexFinishReason(candidate.FinishReason),
			},
		},
		Usage: Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		},
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


// vertexStream implements ChatStream for Vertex AI responses
type vertexStream struct {
	response *http.Response
	reader   *bufio.Reader
	model    string
	closed   bool
}

func newVertexStream(resp *http.Response, model string) *vertexStream {
	return &vertexStream{
		response: resp,
		reader:   bufio.NewReader(resp.Body),
		model:    model,
	}
}

func (s *vertexStream) Recv() (*ChatStreamChunk, error) {
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

		// Parse Vertex AI streaming response
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
						FinishReason: mapVertexFinishReason(chunk.Candidates[0].FinishReason),
					},
				},
			}, nil
		}
	}
}

func (s *vertexStream) Close() error {
	s.closed = true
	return s.response.Body.Close()
}