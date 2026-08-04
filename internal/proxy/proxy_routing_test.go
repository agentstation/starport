package proxy

import (
	"context"
	"io"
	"testing"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

type capturingRouter struct {
	req *routepkg.Request
}

func (r *capturingRouter) SelectModel(context.Context, *routepkg.Request) (string, connectors.Connector, error) {
	return "", nil, nil
}

func (r *capturingRouter) RouteWithFallback(_ context.Context, req *routepkg.Request) (*routepkg.Response, error) {
	r.req = req
	return &routepkg.Response{
		ChatResponse: &connectors.ChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 1,
			Model:   "openai/gpt-4o",
			Choices: []connectors.Choice{
				{
					Index: 0,
					Message: connectors.Message{
						Role:    "assistant",
						Content: "ok",
					},
				},
			},
		},
		ModelUsed: "openai/gpt-4o",
	}, nil
}

func (r *capturingRouter) RouteStream(context.Context, *routepkg.Request) (execution.ManagedStream, error) {
	return nil, nil
}

func TestProcessChatCompletionPassesProviderPreferences(t *testing.T) {
	router := &capturingRouter{}
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model: "openai/gpt-4o",
			User:  "conversation-123",
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
			}},
			Tools: []inference.Tool{{Name: "lookup"}},
		},
		Provider: &ProviderPreferences{
			Order:         []string{"openai", "anthropic"},
			Only:          []string{"openai"},
			Ignore:        []string{"google-ai-studio"},
			AllowFallback: true,
		},
		APIKeyConfig: &APIKeyRoutingConfig{
			AllowedModels: []string{"openai/gpt-4o"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, router.req)
	require.NotNil(t, router.req.ProviderPreferences)
	require.Equal(t, []string{"openai", "anthropic"}, router.req.ProviderPreferences.Order)
	require.Equal(t, []string{"openai"}, router.req.ProviderPreferences.Only)
	require.Equal(t, []string{"google-ai-studio"}, router.req.ProviderPreferences.Ignore)
	require.True(t, router.req.ProviderPreferences.AllowFallbacks)
	require.NotNil(t, router.req.APIKeyConfig)
	require.Equal(t, []string{"openai/gpt-4o"}, router.req.APIKeyConfig.AllowedModels)
	require.NotNil(t, router.req.Metadata)
	require.Equal(t, "conversation-123", router.req.Metadata.ConversationID)
	require.Equal(t, []string{"function_calling"}, router.req.Metadata.RequiredFeatures)
	require.Positive(t, router.req.Metadata.EstimatedTokens)
}

func TestEmbeddingRouteHonorsAPIKeyRestrictions(t *testing.T) {
	route := runtimecatalog.Route{
		DefinitionID: "openai/text-embedding-3-small",
		ProviderID:   "openai", ProviderModelID: "text-embedding-3-small",
	}
	require.True(t, embeddingRouteAllowed("openai/text-embedding-3-small", route, nil))
	require.True(t, embeddingRouteAllowed("openai/text-embedding-3-small", route, &APIKeyRoutingConfig{
		AllowedModels: []string{"openai/text-embedding-3-small"}, AllowedProviders: []string{"openai"},
	}))
	require.False(t, embeddingRouteAllowed("openai/text-embedding-3-small", route, &APIKeyRoutingConfig{
		AllowedModels: []string{"openai/gpt-4.1"},
	}))
	require.False(t, embeddingRouteAllowed("openai/text-embedding-3-small", route, &APIKeyRoutingConfig{
		AllowedModels: []string{"*"}, AllowedProviders: []string{"anthropic"},
	}))
}

type preparingRouter struct {
	calls    int
	received connectors.ChatRequest
}

func (r *preparingRouter) SelectModel(context.Context, *routepkg.Request) (string, connectors.Connector, error) {
	return "", nil, nil
}

func (r *preparingRouter) RouteWithFallback(_ context.Context, req *routepkg.Request) (*routepkg.Response, error) {
	r.calls++
	attempt := *req.ChatRequest
	attempt.Model = "google-ai-studio/gemini-pro"
	if req.PrepareAttempt != nil {
		if prepared := req.PrepareAttempt(routing.Route{ProviderModelID: attempt.Model}, &attempt); prepared != nil {
			attempt = *prepared
		}
	}
	r.received = attempt

	return &routepkg.Response{
		ChatResponse: &connectors.ChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 1,
			Model:   attempt.Model,
			Choices: []connectors.Choice{
				{
					Index: 0,
					Message: connectors.Message{
						Role:    "assistant",
						Content: "ok",
					},
				},
			},
		},
		ModelUsed: attempt.Model,
	}, nil
}

func (r *preparingRouter) RouteStream(context.Context, *routepkg.Request) (execution.ManagedStream, error) {
	return nil, nil
}

func TestProcessChatCompletionStripsCacheControlBeforeSingleUnsupportedProviderAttempt(t *testing.T) {
	router := &preparingRouter{}
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model: "google-ai-studio/gemini-pro",
			Messages: []inference.Message{{
				Role: inference.RoleUser,
				Content: []inference.ContentPart{{
					Kind: inference.ContentText, Text: "hello", CacheControl: "ephemeral",
				}},
			}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, router.calls)
	require.False(t, connectors.HasCacheControl(router.received.Messages[0].Content))
}

type streamCapturingRouter struct {
	request  *routepkg.Request
	received connectors.ChatRequest
	modelID  string
}

func (r *streamCapturingRouter) SelectModel(context.Context, *routepkg.Request) (string, connectors.Connector, error) {
	return "", nil, nil
}

func (r *streamCapturingRouter) RouteWithFallback(context.Context, *routepkg.Request) (*routepkg.Response, error) {
	return nil, nil
}

func (r *streamCapturingRouter) RouteStream(_ context.Context, req *routepkg.Request) (execution.ManagedStream, error) {
	r.request = req
	attempt := *req.ChatRequest
	attempt.Model = r.modelID
	if attempt.Model == "" {
		attempt.Model = "anthropic/claude-3"
	}
	if req.PrepareAttempt != nil {
		if prepared := req.PrepareAttempt(routing.Route{ProviderModelID: attempt.Model}, &attempt); prepared != nil {
			attempt = *prepared
		}
	}
	r.received = attempt
	return &managedEventStream{event: inference.StreamEvent{
		Kind:      inference.StreamDelta,
		Model:     attempt.Model,
		ModelUsed: attempt.Model,
		Deltas:    []inference.ChoiceDelta{{Index: 0, Text: "ok"}},
	}}, nil
}

func TestProcessChatCompletionStreamDelegatesOneRouteRequest(t *testing.T) {
	router := &streamCapturingRouter{}
	service := &proxy{router: router}

	stream, err := service.ProcessChatCompletionStream(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{
			FallbackModels: []string{"openai/gpt-4o", "anthropic/claude-3"},
			Stream:         true,
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
			}},
		},
	})
	require.NoError(t, err)
	defer stream.Close()

	chunk, err := stream.Read()
	require.NoError(t, err)
	require.Equal(t, "anthropic/claude-3", chunk.Model)
	require.NotNil(t, router.request)
	require.Equal(t, []string{"openai/gpt-4o", "anthropic/claude-3"}, router.request.Models)
}

func TestProcessChatCompletionStreamPreparesUnsupportedProviderAttempt(t *testing.T) {
	router := &streamCapturingRouter{modelID: "google-ai-studio/gemini-pro"}
	service := &proxy{router: router}

	stream, err := service.ProcessChatCompletionStream(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model:  "google-ai-studio/gemini-pro",
			Stream: true,
			Messages: []inference.Message{{
				Role: inference.RoleUser,
				Content: []inference.ContentPart{{
					Kind: inference.ContentText, Text: "hello", CacheControl: "ephemeral",
				}},
			}},
		},
	})
	require.NoError(t, err)
	defer stream.Close()
	require.False(t, connectors.HasCacheControl(router.received.Messages[0].Content))
}

type managedEventStream struct {
	event     inference.StreamEvent
	read      bool
	committed bool
}

func (s *managedEventStream) Read() (*inference.StreamEvent, error) {
	if s.read {
		return nil, io.EOF
	}
	s.read = true
	s.committed = true
	event := s.event.Clone()
	return &event, nil
}

func (s *managedEventStream) Close() error                          { return nil }
func (s *managedEventStream) Attempts() []execution.AttemptEvidence { return nil }
func (s *managedEventStream) Committed() bool                       { return s.committed }
func (s *managedEventStream) ModelUsed() string                     { return s.event.ModelUsed }
