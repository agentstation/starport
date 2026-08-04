package connectors

import (
	"context"
	"fmt"
	"net/http"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// GroqConnector implements the Connector interface for Groq
// Groq provides an OpenAI-compatible API, so we can reuse the OpenAI connector
type GroqConnector struct {
	*OpenAIConnector
}

// NewGroqConnector creates a new Groq connector
func NewGroqConnector(config ProviderConfig) (*GroqConnector, error) {
	// Create an OpenAI connector with Groq's base URL
	openAIConnector, err := NewOpenAIConnector(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create groq connector: %w", err)
	}
	openAIConnector.providerID = catalogs.ProviderIDGroq
	openAIConnector.provider = string(catalogs.ProviderIDGroq)

	return &GroqConnector{
		OpenAIConnector: openAIConnector,
	}, nil
}

// Name returns the provider name
func (c *GroqConnector) Name() string {
	return "groq"
}

// Embeddings returns an error as Groq doesn't support embeddings
func (c *GroqConnector) Embeddings(_ context.Context, _ *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	return nil, &APIError{
		StatusCode: http.StatusNotImplemented,
		Message:    "Groq does not support embeddings. Consider using a different provider for embedding generation.",
		Type:       "not_supported",
		Provider:   "groq",
	}
}

// Chat adds Groq stream usage options.
func (c *GroqConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Create a copy of the request to avoid modifying the original
	modifiedReq := *req

	// Add stream_options to include usage data in streaming responses
	if modifiedReq.Stream {
		modifiedReq.StreamOptions = &StreamOptions{
			IncludeUsage: true,
		}
	}

	// Call the parent implementation with the modified request
	return c.OpenAIConnector.Chat(ctx, &modifiedReq)
}

// ChatStream adds Groq stream usage options.
func (c *GroqConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	// Create a copy of the request to avoid modifying the original
	modifiedReq := *req

	// Add stream_options to include usage data in streaming responses
	if modifiedReq.Stream {
		modifiedReq.StreamOptions = &StreamOptions{
			IncludeUsage: true,
		}
	}

	// Call the parent implementation with the modified request
	return c.OpenAIConnector.ChatStream(ctx, &modifiedReq)
}
