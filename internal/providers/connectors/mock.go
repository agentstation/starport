package connectors

import (
	"context"
	"io"
	"sync"
	"time"
)

const (
	mockCompletionID = "chatcmpl-mock"
	mockModel        = "mock-model"
)

// MockConnector implements a mock LLM connector for testing
type MockConnector struct {
	name           string
	config         ProviderConfig
	chatError      error
	streamError    error
	embeddingError error
	chatResponse   *ChatResponse
	streamChunks   []ChatStreamChunk
	// streamRecvError ends the mock stream after its chunks instead of
	// io.EOF, imitating a provider error frame inside a 200 stream.
	streamRecvError error
	embeddingResp   *EmbeddingsResponse
	closed          bool
	mu              sync.RWMutex
}

// NewMockConnector creates a new mock connector
func NewMockConnector(config ProviderConfig) *MockConnector {
	return &MockConnector{
		name:   "mock",
		config: config,
		chatResponse: &ChatResponse{
			ID:      mockCompletionID,
			Object:  objectChatCompletion,
			Created: time.Now().Unix(),
			Model:   mockModel,
			Choices: []Choice{
				{
					Index: 0,
					Message: Message{
						Role:    RoleAssistant,
						Content: "This is a mock response",
					},
					FinishReason: finishReasonStop,
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
				ID:      mockCompletionID,
				Object:  objectChatCompletionChunk,
				Created: time.Now().Unix(),
				Model:   mockModel,
				Choices: []StreamChoice{
					{
						Index: 0,
						Delta: MessageDelta{
							Role: RoleAssistant,
						},
					},
				},
			},
			{
				ID:      mockCompletionID,
				Object:  objectChatCompletionChunk,
				Created: time.Now().Unix(),
				Model:   mockModel,
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
				ID:      mockCompletionID,
				Object:  objectChatCompletionChunk,
				Created: time.Now().Unix(),
				Model:   mockModel,
				Choices: []StreamChoice{
					{
						Index: 0,
						Delta: MessageDelta{
							Content: "mock streaming response",
						},
						FinishReason: finishReasonStop,
					},
				},
			},
		},
		embeddingResp: &EmbeddingsResponse{
			Object: objectList,
			Data: []Embedding{
				{
					Object:    objectEmbedding,
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
		chunks:    m.streamChunks,
		recvError: m.streamRecvError,
		ctx:       ctx,
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

// SetStreamRecvError terminates the mock stream with err after its chunks
// instead of io.EOF, imitating a provider error frame inside a 200 stream.
func (m *MockConnector) SetStreamRecvError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamRecvError = err
}

// mockChatStream implements ChatStream for testing
type mockChatStream struct {
	chunks    []ChatStreamChunk
	recvError error
	index     int
	closed    bool
	ctx       context.Context
	mu        sync.Mutex
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
		if s.recvError != nil {
			return nil, s.recvError
		}
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
