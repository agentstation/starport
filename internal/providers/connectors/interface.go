package connectors

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Provider name constants
const (
	GoogleAIStudioProvider = "google-ai-studio"
	GoogleVertexAIProvider = "google-vertex"
	AzureOpenAIProvider    = "azure-openai"
)

// Connector defines the interface for LLM provider integrations
type Connector interface {
	// Chat performs a chat completion request
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatStream performs a streaming chat completion request
	ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error)

	// Embeddings generates embeddings for the given input
	Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error)

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

// NewConnector delegates construction to the compiled adapter registry.
func NewConnector(provider string, config ProviderConfig) (Connector, error) {
	registry, err := ProductionAdapterRegistry()
	if err != nil {
		return nil, err
	}
	return registry.NewConnector(catalogs.ProviderID(provider), config)
}
