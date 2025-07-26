package connectors

import (
	"context"
	"fmt"
	"time"
)

// Provider name constants
const (
	GoogleAIStudioProvider = "google-ai-studio"
	GoogleVertexAIProvider = "google-vertex"
)

// Connector defines the interface for LLM provider integrations
type Connector interface {
	// Chat performs a chat completion request
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatStream performs a streaming chat completion request
	ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error)

	// Embeddings generates embeddings for the given input
	Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error)

	// Models lists available models from the provider
	Models(ctx context.Context) (*ModelsResponse, error)

	// Health checks the health of the connector
	Health(ctx context.Context) error

	// Name returns the provider name
	Name() string

	// Close cleans up any resources
	Close() error
}

// ChatStream represents a streaming response from the provider
type ChatStream interface {
	// Recv receives the next chunk of the response
	// Returns io.EOF when the stream is complete
	Recv() (*ChatStreamChunk, error)

	// Close closes the stream
	Close() error
}

// NewConnector creates a connector instance for the given provider
// This follows the idiomatic Go pattern of direct instantiation
func NewConnector(provider string, config ProviderConfig) (Connector, error) {
	switch provider {
	case "openai":
		return NewOpenAIConnector(config)
	case "anthropic":
		return NewAnthropicConnector(config)
	case GoogleAIStudioProvider:
		return NewGoogleAIStudioConnector(config)
	case GoogleVertexAIProvider:
		return NewVertexAIConnector(config)
	case "groq":
		return NewGroqConnector(config)
	case "mistral":
		return NewMistralConnector(config)
	case "azure":
		return NewAzureOpenAIConnector(config)
	case "ollama":
		// Only create Ollama connector if explicitly enabled
		if !config.Enabled {
			return nil, fmt.Errorf("ollama support is not enabled (use --enable-ollama flag)")
		}
		return NewOllamaConnector(config)
	case "mock":
		return NewMockConnector(config), nil
	default:
		return nil, ErrProviderNotSupported
	}
}

// HealthChecker provides health checking for connectors
type HealthChecker interface {
	// HealthCheck performs a health check with timeout
	HealthCheck(ctx context.Context, timeout time.Duration) HealthStatus
}

// HealthStatus represents the health of a connector
type HealthStatus struct {
	Healthy   bool          `json:"healthy"`
	Latency   time.Duration `json:"latency"`
	Error     string        `json:"error,omitempty"`
	CheckedAt time.Time     `json:"checked_at"`
}
