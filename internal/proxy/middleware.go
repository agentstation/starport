package proxy

import (
	"context"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/rs/zerolog/log"
)

// Middleware wraps a Proxy with additional functionality.
// Middlewares can be composed to create a chain of handlers.
type Middleware interface {
	// Wrap wraps the given proxy with the middleware functionality
	Wrap(Proxy) Proxy
}

// MiddlewareFunc is a function that implements the Middleware interface.
type MiddlewareFunc func(Proxy) Proxy

// Wrap implements the Middleware interface.
func (f MiddlewareFunc) Wrap(p Proxy) Proxy {
	return f(p)
}

// Chain combines multiple middlewares into a single middleware.
// The middlewares are applied in the order they are provided.
func Chain(middlewares ...Middleware) Middleware {
	return MiddlewareFunc(func(proxy Proxy) Proxy {
		// Apply middlewares in reverse order
		for i := len(middlewares) - 1; i >= 0; i-- {
			proxy = middlewares[i].Wrap(proxy)
		}
		return proxy
	})
}

// loggingService wraps a Proxy with request/response logging.
type loggingService struct {
	proxy Proxy
}

// LoggingMiddleware creates a middleware that logs requests and responses.
func LoggingMiddleware() Middleware {
	return MiddlewareFunc(func(proxy Proxy) Proxy {
		return &loggingService{proxy: proxy}
	})
}

// ProcessChatCompletion logs the request and response.
func (s *loggingService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	start := time.Now()

	log.Info().
		Str("method", "ProcessChatCompletion").
		Str("model", req.Model).
		Int("messages", len(req.Messages)).
		Str("request_id", req.RequestID).
		Msg("processing chat completion request")

	resp, err := s.proxy.ProcessChatCompletion(ctx, req)

	duration := time.Since(start)
	logger := log.Info().
		Str("method", "ProcessChatCompletion").
		Dur("duration", duration).
		Str("request_id", req.RequestID)

	if err != nil {
		logger.Err(err).Msg("chat completion failed")
	} else {
		logger.
			Str("model_used", resp.ModelUsed).
			Int("choices", len(resp.Choices)).
			Msg("chat completion succeeded")
	}

	return resp, err
}

// ProcessChatCompletionStream logs the streaming request.
func (s *loggingService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	start := time.Now()

	log.Info().
		Str("method", "ProcessChatCompletionStream").
		Str("model", req.Model).
		Int("messages", len(req.Messages)).
		Str("request_id", req.RequestID).
		Msg("processing streaming chat completion request")

	stream, err := s.proxy.ProcessChatCompletionStream(ctx, req)

	if err != nil {
		log.Error().
			Str("method", "ProcessChatCompletionStream").
			Dur("duration", time.Since(start)).
			Str("request_id", req.RequestID).
			Err(err).
			Msg("streaming chat completion failed")
		return nil, err
	}

	// Wrap the stream to log when it completes
	return &loggingStream{
		stream:    stream,
		startTime: start,
		requestID: req.RequestID,
	}, nil
}

// ProcessEmbeddings logs the embeddings request and response.
func (s *loggingService) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	start := time.Now()

	log.Info().
		Str("method", "ProcessEmbeddings").
		Str("model", req.Model).
		Str("request_id", req.RequestID).
		Msg("processing embeddings request")

	resp, err := s.proxy.ProcessEmbeddings(ctx, req)

	duration := time.Since(start)
	logger := log.Info().
		Str("method", "ProcessEmbeddings").
		Dur("duration", duration).
		Str("request_id", req.RequestID)

	if err != nil {
		logger.Err(err).Msg("embeddings generation failed")
	} else {
		logger.
			Int("embeddings", len(resp.Data)).
			Msg("embeddings generation succeeded")
	}

	return resp, err
}

// ListModels logs the list models request.
func (s *loggingService) ListModels(ctx context.Context) (*ModelsResponse, error) {
	start := time.Now()

	log.Info().
		Str("method", "ListModels").
		Msg("listing available models")

	resp, err := s.proxy.ListModels(ctx)

	duration := time.Since(start)
	logger := log.Info().
		Str("method", "ListModels").
		Dur("duration", duration)

	if err != nil {
		logger.Err(err).Msg("list models failed")
	} else {
		logger.
			Int("models", len(resp.Data)).
			Msg("list models succeeded")
	}

	return resp, err
}

// ListProviders logs the list providers request.
func (s *loggingService) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	start := time.Now()

	log.Info().
		Str("method", "ListProviders").
		Msg("listing available providers")

	resp, err := s.proxy.ListProviders(ctx)

	duration := time.Since(start)
	logger := log.Info().
		Str("method", "ListProviders").
		Dur("duration", duration)

	if err != nil {
		logger.Err(err).Msg("list providers failed")
	} else {
		logger.
			Int("providers", len(resp.Providers)).
			Msg("list providers succeeded")
	}

	return resp, err
}

// GetModelEndpoints logs the get model endpoints request.
func (s *loggingService) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	start := time.Now()

	log.Info().
		Str("method", "GetModelEndpoints").
		Str("model_id", modelID).
		Msg("getting model endpoints")

	resp, err := s.proxy.GetModelEndpoints(ctx, modelID)

	duration := time.Since(start)
	logger := log.Info().
		Str("method", "GetModelEndpoints").
		Str("model_id", modelID).
		Dur("duration", duration)

	if err != nil {
		logger.Err(err).Msg("get model endpoints failed")
	} else {
		logger.
			Int("endpoints", len(resp.Endpoints)).
			Msg("get model endpoints succeeded")
	}

	return resp, err
}

// loggingStream wraps a stream to log when it completes.
type loggingStream struct {
	stream    ChatCompletionStreamResponse
	startTime time.Time
	requestID string
}

// Read passes through to the underlying stream.
func (s *loggingStream) Read() (*connectors.ChatStreamChunk, error) {
	return s.stream.Read()
}

// Close logs the stream completion and closes the underlying stream.
func (s *loggingStream) Close() error {
	err := s.stream.Close()

	log.Info().
		Str("method", "ProcessChatCompletionStream").
		Dur("duration", time.Since(s.startTime)).
		Str("request_id", s.requestID).
		Err(err).
		Msg("streaming chat completion completed")

	return err
}

// TimingMiddleware creates a middleware that adds timing information to context.
func TimingMiddleware() Middleware {
	return MiddlewareFunc(func(proxy Proxy) Proxy {
		return &timingService{proxy: proxy}
	})
}

// timingService adds request timing to context.
type timingService struct {
	proxy Proxy
}

// Define context key for timing
type timingKey struct{}

// GetRequestStartTime retrieves the request start time from context.
func GetRequestStartTime(ctx context.Context) (time.Time, bool) {
	start, ok := ctx.Value(timingKey{}).(time.Time)
	return start, ok
}

// ProcessChatCompletion adds timing to context.
func (s *timingService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	ctx = context.WithValue(ctx, timingKey{}, time.Now())
	return s.proxy.ProcessChatCompletion(ctx, req)
}

// ProcessChatCompletionStream adds timing to context.
func (s *timingService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	ctx = context.WithValue(ctx, timingKey{}, time.Now())
	return s.proxy.ProcessChatCompletionStream(ctx, req)
}

// ProcessEmbeddings adds timing to context.
func (s *timingService) ProcessEmbeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	ctx = context.WithValue(ctx, timingKey{}, time.Now())
	return s.proxy.ProcessEmbeddings(ctx, req)
}

// ListModels adds timing to context.
func (s *timingService) ListModels(ctx context.Context) (*ModelsResponse, error) {
	ctx = context.WithValue(ctx, timingKey{}, time.Now())
	return s.proxy.ListModels(ctx)
}

// ListProviders adds timing to context.
func (s *timingService) ListProviders(ctx context.Context) (*ProvidersResponse, error) {
	ctx = context.WithValue(ctx, timingKey{}, time.Now())
	return s.proxy.ListProviders(ctx)
}

// GetModelEndpoints adds timing to context.
func (s *timingService) GetModelEndpoints(ctx context.Context, modelID string) (*ModelEndpointsResponse, error) {
	ctx = context.WithValue(ctx, timingKey{}, time.Now())
	return s.proxy.GetModelEndpoints(ctx, modelID)
}
