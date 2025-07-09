package connectors

import (
	"context"
	"io"
	"sync"
	"time"
)

// MockConnector implements a mock LLM connector for testing
type MockConnector struct {
	name           string
	config         ProviderConfig
	healthError    error
	chatError      error
	streamError    error
	embeddingError error
	modelsError    error
	chatResponse   *ChatResponse
	streamChunks   []ChatStreamChunk
	embeddingResp  *EmbeddingsResponse
	modelsResp     *ModelsResponse
	closed         bool
	mu             sync.RWMutex
}

// NewMockConnector creates a new mock connector
func NewMockConnector(config ProviderConfig) *MockConnector {
	return &MockConnector{
		name:   "mock",
		config: config,
		chatResponse: &ChatResponse{
			ID:      "chatcmpl-mock",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "mock-model",
			Choices: []Choice{
				{
					Index: 0,
					Message: Message{
						Role:    "assistant",
						Content: "This is a mock response",
					},
					FinishReason: "stop",
				},
			},
			Usage: Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
		streamChunks: []ChatStreamChunk{
			{
				ID:      "chatcmpl-mock",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "mock-model",
				Choices: []StreamChoice{
					{
						Index: 0,
						Delta: MessageDelta{
							Role: "assistant",
						},
					},
				},
			},
			{
				ID:      "chatcmpl-mock",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "mock-model",
				Choices: []StreamChoice{
					{
						Index: 0,
						Delta: MessageDelta{
							Content: "This is a ",
						},
					},
				},
			},
			{
				ID:      "chatcmpl-mock",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "mock-model",
				Choices: []StreamChoice{
					{
						Index: 0,
						Delta: MessageDelta{
							Content: "mock streaming response",
						},
						FinishReason: "stop",
					},
				},
			},
		},
		embeddingResp: &EmbeddingsResponse{
			Object: "list",
			Data: []Embedding{
				{
					Object:    "embedding",
					Index:     0,
					Embedding: []float32{0.1, 0.2, 0.3},
				},
			},
			Model: "mock-embedding-model",
			Usage: Usage{
				PromptTokens: 5,
				TotalTokens:  5,
			},
		},
		modelsResp: &ModelsResponse{
			Object: "list",
			Data: []Model{
				{
					ID:      "mock-model",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "mock",
				},
				{
					ID:      "mock-embedding-model",
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "mock",
				},
			},
		},
	}
}

// Chat performs a mock chat completion request
func (m *MockConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	_ = req // req is intentionally unused in mock
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrConnectorClosed
	}

	if m.chatError != nil {
		return nil, m.chatError
	}

	// Simulate processing time
	select {
	case <-time.After(10 * time.Millisecond):
		return m.chatResponse, nil
	case <-ctx.Done():
		return nil, ErrContextCanceled
	}
}

// ChatStream performs a mock streaming chat completion request
func (m *MockConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	_ = req // req is intentionally unused in mock
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrConnectorClosed
	}

	if m.streamError != nil {
		return nil, m.streamError
	}

	return &mockChatStream{
		chunks: m.streamChunks,
		ctx:    ctx,
	}, nil
}

// Embeddings generates mock embeddings
func (m *MockConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	_ = req // req is intentionally unused in mock
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrConnectorClosed
	}

	if m.embeddingError != nil {
		return nil, m.embeddingError
	}

	// Simulate processing time
	select {
	case <-time.After(10 * time.Millisecond):
		return m.embeddingResp, nil
	case <-ctx.Done():
		return nil, ErrContextCanceled
	}
}

// Models lists available models
func (m *MockConnector) Models(ctx context.Context) (*ModelsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrConnectorClosed
	}

	if m.modelsError != nil {
		return nil, m.modelsError
	}

	// Simulate processing time
	select {
	case <-time.After(5 * time.Millisecond):
		return m.modelsResp, nil
	case <-ctx.Done():
		return nil, ErrContextCanceled
	}
}

// Health checks the health of the mock connector
func (m *MockConnector) Health(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return ErrConnectorClosed
	}

	if m.healthError != nil {
		return m.healthError
	}

	// Simulate health check
	select {
	case <-time.After(5 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ErrContextCanceled
	}
}

// Name returns the provider name
func (m *MockConnector) Name() string {
	return m.name
}

// Close closes the mock connector
func (m *MockConnector) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// Mock-specific methods for testing

// SetHealthError sets an error to be returned by Health()
func (m *MockConnector) SetHealthError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthError = err
}

// SetChatError sets an error to be returned by Chat()
func (m *MockConnector) SetChatError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatError = err
}

// SetStreamError sets an error to be returned by ChatStream()
func (m *MockConnector) SetStreamError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamError = err
}

// SetChatResponse sets the response to be returned by Chat()
func (m *MockConnector) SetChatResponse(resp *ChatResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatResponse = resp
}

// SetStreamChunks sets the chunks to be returned by ChatStream()
func (m *MockConnector) SetStreamChunks(chunks []ChatStreamChunk) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamChunks = chunks
}

// mockChatStream implements ChatStream for testing
type mockChatStream struct {
	chunks []ChatStreamChunk
	index  int
	closed bool
	ctx    context.Context
	mu     sync.Mutex
}

// Recv receives the next chunk
func (s *mockChatStream) Recv() (*ChatStreamChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrStreamClosed
	}

	// Check context
	select {
	case <-s.ctx.Done():
		return nil, ErrContextCanceled
	default:
	}

	if s.index >= len(s.chunks) {
		return nil, io.EOF
	}

	chunk := s.chunks[s.index]
	s.index++

	// Simulate streaming delay
	time.Sleep(5 * time.Millisecond)

	return &chunk, nil
}

// Close closes the stream
func (s *mockChatStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

