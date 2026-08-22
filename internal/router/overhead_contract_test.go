package router

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

// The overhead timer contract: time spent inside a provider connector is an
// upstream wait, never gateway overhead. A connector that sleeps 500ms must
// leave the measured overhead near zero.

func TestRouteWithFallbackOverheadExcludesUpstreamDelay(t *testing.T) {
	slow := &mockConnector{
		name: "openai",
		chatFunc: func(_ context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			time.Sleep(500 * time.Millisecond)
			return &connectors.ChatResponse{
				ID:    "chatcmpl-slow",
				Model: req.Model,
				Choices: []connectors.Choice{
					{Message: connectors.Message{Role: "assistant", Content: "ok"}},
				},
			}, nil
		},
	}
	router := New(&mockRegistry{connectors: map[string]connectors.Connector{"openai": slow}})

	ctx, timer := execution.WithOverheadTimer(context.Background())
	_, err := router.RouteWithFallback(ctx, &Request{
		ChatRequest: &connectors.ChatRequest{
			Model:    "openai/gpt-4",
			Messages: []connectors.Message{{Role: "user", Content: "hello"}},
		},
	})
	require.NoError(t, err)
	require.Less(t, timer.OverheadMS(), int64(50),
		"a 500ms upstream call must not count as gateway overhead")
}

func TestRouteStreamOverheadExcludesUpstreamDelay(t *testing.T) {
	slow := &mockConnector{
		name: "openai",
		chatStreamFunc: func(context.Context, *connectors.ChatRequest) (connectors.ChatStream, error) {
			time.Sleep(250 * time.Millisecond)
			return &delayedChatStream{
				delay: 250 * time.Millisecond,
				chunks: []connectors.ChatStreamChunk{{
					ID:    "chatcmpl-slow-stream",
					Model: "gpt-4",
					Choices: []connectors.StreamChoice{{
						Index: 0,
						Delta: connectors.MessageDelta{Content: "ok"},
					}},
				}},
			}, nil
		},
	}
	router := New(&mockRegistry{connectors: map[string]connectors.Connector{"openai": slow}})

	ctx, timer := execution.WithOverheadTimer(context.Background())
	stream, err := router.RouteStream(ctx, &Request{
		ChatRequest: &connectors.ChatRequest{
			Model:    "openai/gpt-4",
			Messages: []connectors.Message{{Role: "user", Content: "hello"}},
			Stream:   true,
		},
	})
	require.NoError(t, err)
	defer stream.Close()

	for {
		_, readErr := stream.Read()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
	}
	require.Less(t, timer.OverheadMS(), int64(50),
		"stream open and receive waits must not count as gateway overhead")
}

// delayedChatStream sleeps before every receive to simulate a slow provider.
type delayedChatStream struct {
	delay  time.Duration
	chunks []connectors.ChatStreamChunk
}

func (s *delayedChatStream) Recv() (*connectors.ChatStreamChunk, error) {
	time.Sleep(s.delay)
	if len(s.chunks) == 0 {
		return nil, io.EOF
	}
	chunk := s.chunks[0]
	s.chunks = s.chunks[1:]
	return &chunk, nil
}

func (s *delayedChatStream) Close() error { return nil }
