package connectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// OllamaConnector implements the Connector interface for Ollama
type OllamaConnector struct {
	config     ProviderConfig
	httpClient *http.Client
	provider   string
}

// NewOllamaConnector creates a new Ollama connector
func NewOllamaConnector(config ProviderConfig) (*OllamaConnector, error) {
	return newOllamaConnector(string(catalogs.ProviderIDOllama), config)
}

func newOllamaConnector(provider string, config ProviderConfig) (*OllamaConnector, error) {
	// Set default base URL if not provided
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	httpClient, err := newProviderHTTPClient(provider, config)
	if err != nil {
		return nil, err
	}

	return &OllamaConnector{
		config:     config,
		httpClient: httpClient,
		provider:   provider,
	}, nil
}

// Name returns the provider name
func (c *OllamaConnector) Name() string {
	return c.provider
}

// Chat performs a chat completion request
func (c *OllamaConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Convert to Ollama format
	ollamaReq := map[string]any{
		wireModelToken:    req.Model,
		wireFieldMessages: req.Messages,
		wireFieldStream:   false,
		"options": map[string]any{
			wireFieldTemperature: req.Temperature,
		},
	}

	if req.MaxTokens != nil {
		ollamaReq["options"].(map[string]any)["num_predict"] = *req.MaxTokens
	}

	if req.TopP != nil {
		ollamaReq["options"].(map[string]any)["top_p"] = *req.TopP
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOllama)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := applyRequestAuthentication(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := doRequest(c.httpClient, httpReq)
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
		Object:  objectChatCompletion,
		Created: time.Now().Unix(),
		Model:   ollamaResp.Model,
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    ollamaResp.Message.Role,
					Content: ollamaResp.Message.Content,
				},
				FinishReason: finishReasonStop,
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
	// Convert to Ollama format
	ollamaReq := map[string]any{
		wireModelToken:    req.Model,
		wireFieldMessages: req.Messages,
		wireFieldStream:   true,
		"options": map[string]any{
			wireFieldTemperature: req.Temperature,
		},
	}

	if req.MaxTokens != nil {
		ollamaReq["options"].(map[string]any)["num_predict"] = *req.MaxTokens
	}

	if req.TopP != nil {
		ollamaReq["options"].(map[string]any)["top_p"] = *req.TopP
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOllama)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := applyRequestAuthentication(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := doRequest(c.httpClient, httpReq)
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
		model:  req.Model,
	}, nil
}

// Embeddings generates embeddings for the given input
func (c *OllamaConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	// Ollama supports embeddings via /api/embeddings endpoint
	ollamaReq := map[string]any{
		wireModelToken: req.Model,
		"prompt":       req.Input,
	}

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOllama)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := applyRequestAuthentication(req.Credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := doRequest(c.httpClient, httpReq)
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
		Object: objectList,
		Data: []Embedding{
			{
				Object:    objectEmbedding,
				Embedding: ollamaResp.Embedding,
				Index:     0,
			},
		},
		Model: req.Model,
		Usage: Usage{
			PromptTokens: 0, // Ollama doesn't report token usage for embeddings
			TotalTokens:  0,
		},
	}, nil
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
			Provider:   c.provider,
		}
	}

	// Fallback error
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    string(body),
		Provider:   c.provider,
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
				Object:  objectChatCompletionChunk,
				Created: time.Now().Unix(),
				Model:   s.model,
				Choices: []StreamChoice{
					{
						Index:        0,
						Delta:        MessageDelta{},
						FinishReason: finishReasonStop,
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
		Object:  objectChatCompletionChunk,
		Created: time.Now().Unix(),
		Model:   s.model,
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
