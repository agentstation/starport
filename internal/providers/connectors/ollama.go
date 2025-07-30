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
	"sync"
	"time"

	"github.com/agentstation/starport/pkg/httpclient"
)

// OllamaConnector implements the Connector interface for Ollama
type OllamaConnector struct {
	config     ProviderConfig
	httpClient *http.Client

	// Cache for model list with TTL
	modelListMu    sync.RWMutex
	modelListCache *ModelsResponse
	modelListTime  time.Time
	modelListTTL   time.Duration
}

// NewOllamaConnector creates a new Ollama connector
func NewOllamaConnector(config ProviderConfig) (*OllamaConnector, error) {
	// Set default base URL if not provided
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:11434"
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create HTTP client using httpclient package
	// Ollama typically runs locally, so we can use more aggressive connection settings
	ollamaConfig := httpclient.DefaultConfig()
	ollamaConfig.MaxConnsPerHost = 50         // Lower for local service
	ollamaConfig.MaxIdleConnsPerHost = 20     // Lower for local service
	ollamaConfig.EnableCircuitBreaker = false // Disable for local service

	client, err := httpclient.New("ollama", ollamaConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return &OllamaConnector{
		config:       config,
		httpClient:   client.GetHTTPClient(),
		modelListTTL: 5 * time.Minute, // Cache model list for 5 minutes
	}, nil
}

// Name returns the provider name
func (c *OllamaConnector) Name() string {
	return "ollama"
}

// Chat performs a chat completion request
func (c *OllamaConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Strip provider prefix from model name if present
	model := strings.TrimPrefix(req.Model, "ollama/")

	// Convert to Ollama format
	ollamaReq := map[string]interface{}{
		"model":    model,
		"messages": req.Messages,
		"stream":   false,
		"options": map[string]interface{}{
			"temperature": req.Temperature,
		},
	}

	if req.MaxTokens != nil {
		ollamaReq["options"].(map[string]interface{})["num_predict"] = *req.MaxTokens
	}

	if req.TopP != nil {
		ollamaReq["options"].(map[string]interface{})["top_p"] = *req.TopP
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	// Parse Ollama response
	var ollamaResp struct {
		Model     string `json:"model"`
		CreatedAt string `json:"created_at"`
		Message   struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		TotalDuration      int64 `json:"total_duration"`
		LoadDuration       int64 `json:"load_duration"`
		PromptEvalCount    int   `json:"prompt_eval_count"`
		PromptEvalDuration int64 `json:"prompt_eval_duration"`
		EvalCount          int   `json:"eval_count"`
		EvalDuration       int64 `json:"eval_duration"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to OpenAI format
	return &ChatResponse{
		ID:      fmt.Sprintf("ollama-%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "ollama/" + ollamaResp.Model,
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    ollamaResp.Message.Role,
					Content: ollamaResp.Message.Content,
				},
				FinishReason: "stop",
			},
		},
		Usage: Usage{
			PromptTokens:     ollamaResp.PromptEvalCount,
			CompletionTokens: ollamaResp.EvalCount,
			TotalTokens:      ollamaResp.PromptEvalCount + ollamaResp.EvalCount,
		},
	}, nil
}

// ChatStream performs a streaming chat completion request
func (c *OllamaConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	// Strip provider prefix from model name if present
	model := strings.TrimPrefix(req.Model, "ollama/")

	// Convert to Ollama format
	ollamaReq := map[string]interface{}{
		"model":    model,
		"messages": req.Messages,
		"stream":   true,
		"options": map[string]interface{}{
			"temperature": req.Temperature,
		},
	}

	if req.MaxTokens != nil {
		ollamaReq["options"].(map[string]interface{})["num_predict"] = *req.MaxTokens
	}

	if req.TopP != nil {
		ollamaReq["options"].(map[string]interface{})["top_p"] = *req.TopP
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, c.handleError(resp)
	}

	return &ollamaStream{
		resp:   resp,
		reader: bufio.NewReader(resp.Body),
		model:  model,
	}, nil
}

// Embeddings generates embeddings for the given input
func (c *OllamaConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Ollama supports embeddings via /api/embeddings endpoint
	model := strings.TrimPrefix(req.Model, "ollama/")

	ollamaReq := map[string]interface{}{
		"model":  model,
		"prompt": req.Input,
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	// Parse Ollama response
	var ollamaResp struct {
		Embedding []float32 `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to OpenAI format
	return &EmbeddingsResponse{
		Object: "list",
		Data: []Embedding{
			{
				Object:    "embedding",
				Embedding: ollamaResp.Embedding,
				Index:     0,
			},
		},
		Model: "ollama/" + model,
		Usage: Usage{
			PromptTokens: 0, // Ollama doesn't report token usage for embeddings
			TotalTokens:  0,
		},
	}, nil
}

// Models lists available models from the provider
func (c *OllamaConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	// Check cache first
	c.modelListMu.RLock()
	if c.modelListCache != nil && time.Since(c.modelListTime) < c.modelListTTL {
		defer c.modelListMu.RUnlock()
		return c.modelListCache, nil
	}
	c.modelListMu.RUnlock()

	// Fetch fresh model list
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.config.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	// Parse Ollama response
	var ollamaResp struct {
		Models []struct {
			Name       string    `json:"name"`
			ModifiedAt time.Time `json:"modified_at"`
			Size       int64     `json:"size"`
			Digest     string    `json:"digest"`
			Details    struct {
				Format            string   `json:"format"`
				Family            string   `json:"family"`
				Families          []string `json:"families"`
				ParameterSize     string   `json:"parameter_size"`
				QuantizationLevel string   `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to OpenAI format
	models := make([]Model, len(ollamaResp.Models))
	for i, m := range ollamaResp.Models {
		models[i] = Model{
			ID:      "ollama/" + m.Name,
			Object:  "model",
			Created: m.ModifiedAt.Unix(),
			OwnedBy: "ollama",
		}
	}

	response := &ModelsResponse{
		Object: "list",
		Data:   models,
	}

	// Update cache
	c.modelListMu.Lock()
	c.modelListCache = response
	c.modelListTime = time.Now()
	c.modelListMu.Unlock()

	return response, nil
}

// Health checks the health of the connector
func (c *OllamaConnector) Health(ctx context.Context) error {
	// Use tags endpoint as health check
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.config.BaseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrHealthCheckFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrHealthCheckFailed, resp.StatusCode)
	}

	return nil
}

// Close closes the connector
func (c *OllamaConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// handleError handles Ollama-specific error responses
func (c *OllamaConnector) handleError(resp *http.Response) error {
	var errResp struct {
		Error string `json:"error"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != "" {
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    errResp.Error,
			Provider:   "ollama",
		}
	}

	// Fallback error
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    string(body),
		Provider:   "ollama",
	}
}

// ollamaStream implements ChatStream for Ollama streaming responses
type ollamaStream struct {
	resp   *http.Response
	reader *bufio.Reader
	model  string
	closed bool
	mu     sync.Mutex
	// Track usage data to send in final chunk
	promptTokens     int
	completionTokens int
}

func (s *ollamaStream) Recv() (*ChatStreamChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrStreamClosed
	}

	// Read next line
	line, err := s.reader.ReadBytes('\n')
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, &StreamError{Err: err}
	}

	// Parse JSON response
	var ollamaChunk struct {
		Model     string `json:"model"`
		CreatedAt string `json:"created_at"`
		Message   struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Done            bool   `json:"done"`
		DoneReason      string `json:"done_reason,omitempty"`
		PromptEvalCount int    `json:"prompt_eval_count,omitempty"`
		EvalCount       int    `json:"eval_count,omitempty"`
	}

	if err := json.Unmarshal(line, &ollamaChunk); err != nil {
		return nil, &StreamError{Err: err}
	}

	// Check if stream is done
	if ollamaChunk.Done {
		// Capture usage data before returning EOF
		if ollamaChunk.PromptEvalCount > 0 {
			s.promptTokens = ollamaChunk.PromptEvalCount
		}
		if ollamaChunk.EvalCount > 0 {
			s.completionTokens = ollamaChunk.EvalCount
		}

		// Send a final chunk with usage data if we have it
		if s.promptTokens > 0 || s.completionTokens > 0 {
			return &ChatStreamChunk{
				ID:      fmt.Sprintf("ollama-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "ollama/" + s.model,
				Choices: []StreamChoice{
					{
						Index:        0,
						Delta:        MessageDelta{},
						FinishReason: "stop",
					},
				},
				Usage: &Usage{
					PromptTokens:     s.promptTokens,
					CompletionTokens: s.completionTokens,
					TotalTokens:      s.promptTokens + s.completionTokens,
				},
			}, nil
		}

		return nil, io.EOF
	}

	// Convert to OpenAI format
	return &ChatStreamChunk{
		ID:      fmt.Sprintf("ollama-%d", time.Now().UnixNano()),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   "ollama/" + s.model,
		Choices: []StreamChoice{
			{
				Index: 0,
				Delta: MessageDelta{
					Content: ollamaChunk.Message.Content,
				},
			},
		},
	}, nil
}

func (s *ollamaStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	return s.resp.Body.Close()
}
