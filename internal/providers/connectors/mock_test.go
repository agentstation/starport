package connectors_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/providers/connectors"
)

func TestMockConnector(t *testing.T) {
	config := connectors.ProviderConfig{
		BaseURL: "http://mock.api",
		APIKey:  "test-key",
	}

	t.Run("Basic operations", func(t *testing.T) {
		mock := connectors.NewMockConnector(config)
		defer mock.Close()

		if mock.Name() != "mock" {
			t.Errorf("expected name 'mock', got %s", mock.Name())
		}

	})

	t.Run("Chat with custom response", func(t *testing.T) {
		mock := connectors.NewMockConnector(config)
		defer mock.Close()

		customResp := &connectors.ChatResponse{
			ID:      "custom-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "custom-model",
			Choices: []connectors.Choice{
				{
					Index: 0,
					Message: connectors.Message{
						Role:    "assistant",
						Content: "Custom response",
					},
					FinishReason: "stop",
				},
			},
		}

		mock.SetChatResponse(customResp)

		ctx := context.Background()
		req := &connectors.ChatRequest{
			Model: "test",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}

		resp, err := mock.Chat(ctx, req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if resp.ID != "custom-123" {
			t.Errorf("expected ID 'custom-123', got %s", resp.ID)
		}
		if resp.Model != "custom-model" {
			t.Errorf("expected model 'custom-model', got %s", resp.Model)
		}
	})

	t.Run("Chat with error", func(t *testing.T) {
		mock := connectors.NewMockConnector(config)
		defer mock.Close()

		expectedErr := &connectors.APIError{
			Provider:   "mock",
			StatusCode: 500,
			Message:    "Internal error",
		}
		mock.SetChatError(expectedErr)

		ctx := context.Background()
		req := &connectors.ChatRequest{
			Model: "test",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}

		_, err := mock.Chat(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("Stream with custom chunks", func(t *testing.T) {
		mock := connectors.NewMockConnector(config)
		defer mock.Close()

		customChunks := []connectors.ChatStreamChunk{
			{
				ID:      "stream-123",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "stream-model",
				Choices: []connectors.StreamChoice{
					{
						Index: 0,
						Delta: connectors.MessageDelta{
							Content: "First chunk",
						},
					},
				},
			},
			{
				ID:      "stream-123",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "stream-model",
				Choices: []connectors.StreamChoice{
					{
						Index: 0,
						Delta: connectors.MessageDelta{
							Content: "Second chunk",
						},
						FinishReason: "stop",
					},
				},
			},
		}

		mock.SetStreamChunks(customChunks)

		ctx := context.Background()
		req := &connectors.ChatRequest{
			Model:  "test",
			Stream: true,
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}

		stream, err := mock.ChatStream(ctx, req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer stream.Close()

		chunks := []connectors.ChatStreamChunk{}
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			chunks = append(chunks, *chunk)
		}

		if len(chunks) != 2 {
			t.Errorf("expected 2 chunks, got %d", len(chunks))
		}
		if chunks[0].Choices[0].Delta.Content != "First chunk" {
			t.Errorf("expected 'First chunk', got %s", chunks[0].Choices[0].Delta.Content)
		}
	})

	t.Run("Stream error", func(t *testing.T) {
		mock := connectors.NewMockConnector(config)
		defer mock.Close()

		streamErr := errors.New("stream failed")
		mock.SetStreamError(streamErr)

		ctx := context.Background()
		req := &connectors.ChatRequest{
			Model:  "test",
			Stream: true,
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}

		_, err := mock.ChatStream(ctx, req)
		if !errors.Is(err, streamErr) {
			t.Errorf("expected error %v, got %v", streamErr, err)
		}
	})

	t.Run("Closed connector", func(t *testing.T) {
		mock := connectors.NewMockConnector(config)

		// Close the connector
		if err := mock.Close(); err != nil {
			t.Fatalf("failed to close connector: %v", err)
		}

		ctx := context.Background()
		req := &connectors.ChatRequest{
			Model: "test",
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}

		// All operations should fail after close
		if _, err := mock.Chat(ctx, req); err == nil {
			t.Error("expected error for closed connector")
		}

		if _, err := mock.ChatStream(ctx, req); err == nil {
			t.Error("expected error for closed connector")
		}

	})

	t.Run("Stream close", func(t *testing.T) {
		mock := connectors.NewMockConnector(config)
		defer mock.Close()

		ctx := context.Background()
		req := &connectors.ChatRequest{
			Model:  "test",
			Stream: true,
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}

		stream, err := mock.ChatStream(ctx, req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Close the stream
		if err := stream.Close(); err != nil {
			t.Errorf("failed to close stream: %v", err)
		}

		// Recv should fail after close
		_, err = stream.Recv()
		if !errors.Is(err, connectors.ErrStreamClosed) {
			t.Errorf("expected ErrStreamClosed, got %v", err)
		}
	})

	t.Run("Context cancellation in stream", func(t *testing.T) {
		mock := connectors.NewMockConnector(config)
		defer mock.Close()

		ctx, cancel := context.WithCancel(context.Background())

		req := &connectors.ChatRequest{
			Model:  "test",
			Stream: true,
			Messages: []connectors.Message{
				{Role: "user", Content: "Hello"},
			},
		}

		stream, err := mock.ChatStream(ctx, req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer stream.Close()

		// Cancel context after getting stream
		cancel()

		// Next recv should fail
		_, err = stream.Recv()
		if !errors.Is(err, connectors.ErrContextCanceled) {
			t.Errorf("expected ErrContextCanceled, got %v", err)
		}
	})
}
