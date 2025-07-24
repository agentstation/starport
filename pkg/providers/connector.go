package providers

import (
	"context"
	"io"

	"github.com/agentstation/starport/pkg/models"
)

// Connector defines the interface for LLM provider implementations.
// It is embedded in the Provider struct to provide a clean API.
// Implementations should not store references back to the Provider.
type Connector interface {
	// Chat performs a non-streaming chat completion.
	// The model parameter should be validated against available models.
	Chat(ctx context.Context, model string, req *ChatRequest) (*ChatResponse, error)

	// ChatStream performs a streaming chat completion.
	// The returned ChatStream must be closed by the caller.
	ChatStream(ctx context.Context, model string, req *ChatRequest) (ChatStream, error)

	// Embeddings generates embeddings for the given input.
	// The input can be a string or []string.
	Embeddings(ctx context.Context, model string, req *EmbeddingsRequest) (*EmbeddingsResponse, error)
}

// ChatStream represents a streaming chat response.
// Implementations must be safe for concurrent use.
type ChatStream interface {
	// Close closes the stream and releases resources
	io.Closer

	// Next returns the next chunk in the stream.
	// Returns io.EOF when the stream is complete.
	Next() (*ChatChunk, error)
}

// OptionalConnector defines optional methods that connectors may implement
type OptionalConnector interface {
	// Images generates images (optional)
	Images(ctx context.Context, model string, req *ImagesRequest) (*ImagesResponse, error)

	// Audio performs audio transcription or generation (optional)
	Audio(ctx context.Context, model string, req *AudioRequest) (*AudioResponse, error)

	// Moderations performs content moderation (optional)
	Moderations(ctx context.Context, model string, req *ModerationsRequest) (*ModerationsResponse, error)
}

// HealthChecker is an optional interface for connectors that support health checks
type HealthChecker interface {
	// HealthCheck verifies the connector can reach its API endpoint
	HealthCheck(ctx context.Context) error
}

// ModelLister is an optional interface for connectors that can list available models
type ModelLister interface {
	// ListModels returns the current list of available models from the API
	ListModels(ctx context.Context) ([]*models.Model, error)
}