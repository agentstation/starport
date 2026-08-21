package proxy

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/tokenize"
)

// eventListStream replays scripted events. When finalWithEOF is set, the
// last event arrives together with io.EOF, the way connector streams end.
type eventListStream struct {
	events       []inference.StreamEvent
	finalWithEOF bool
	next         int
	closed       bool
}

func (s *eventListStream) Read() (*inference.StreamEvent, error) {
	if s.next >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.next].Clone()
	s.next++
	if s.finalWithEOF && s.next == len(s.events) {
		return &event, io.EOF
	}
	return &event, nil
}

func (s *eventListStream) Close() error {
	s.closed = true
	return nil
}

func drainStream(t *testing.T, stream ChatCompletionStreamResponse) []*inference.StreamEvent {
	t.Helper()
	var events []*inference.StreamEvent
	for {
		event, err := stream.Read()
		if event != nil {
			events = append(events, event)
		}
		if err == io.EOF {
			return events
		}
		require.NoError(t, err)
	}
}

func promptMessages() []inference.Message {
	return []inference.Message{{
		Role:    inference.RoleUser,
		Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "Tell me about starports."}},
	}}
}

func TestUsageNormalizingStreamSynthesizesEstimatedUsage(t *testing.T) {
	inner := &eventListStream{events: []inference.StreamEvent{
		{
			Kind:        inference.StreamDelta,
			ID:          "chatcmpl-1",
			CreatedUnix: 1700000000,
			Model:       "groq/llama-3.3-70b-versatile",
			ModelUsed:   "groq/llama-3.3-70b-versatile",
			Deltas:      []inference.ChoiceDelta{{Role: inference.RoleAssistant, Text: "A starport is "}},
		},
		{
			Kind:   inference.StreamDelta,
			Deltas: []inference.ChoiceDelta{{Text: "a gateway between worlds.", FinishReason: "stop"}},
		},
	}}
	stream := newUsageNormalizingStream(inner, promptMessages(), tokenize.NewEstimator())

	events := drainStream(t, stream)
	require.Len(t, events, 3)

	usage := events[2]
	require.Equal(t, inference.StreamUsage, usage.Kind)
	require.NotNil(t, usage.Usage)
	assert.True(t, usage.Usage.Estimated)
	assert.Greater(t, usage.Usage.InputTokens, 0)
	assert.Greater(t, usage.Usage.OutputTokens, 0)
	assert.Equal(t, usage.Usage.InputTokens+usage.Usage.OutputTokens, usage.Usage.TotalTokens)
	assert.Zero(t, usage.Usage.ReasoningTokens)
	// Identity latched from the deltas so clients see one coherent stream.
	assert.Equal(t, "chatcmpl-1", usage.ID)
	assert.Equal(t, int64(1700000000), usage.CreatedUnix)
	assert.Equal(t, "groq/llama-3.3-70b-versatile", usage.Model)
	assert.Equal(t, "groq/llama-3.3-70b-versatile", usage.ModelUsed)
}

func TestUsageNormalizingStreamCountsReasoning(t *testing.T) {
	inner := &eventListStream{events: []inference.StreamEvent{
		{
			Kind: inference.StreamDelta,
			Deltas: []inference.ChoiceDelta{{
				Reasoning: "Consider what a starport does before answering.",
			}},
		},
		{
			Kind:   inference.StreamDelta,
			Deltas: []inference.ChoiceDelta{{Text: "It routes ships.", FinishReason: "stop"}},
		},
	}}
	stream := newUsageNormalizingStream(inner, promptMessages(), tokenize.NewEstimator())

	events := drainStream(t, stream)
	usage := events[len(events)-1].Usage
	require.NotNil(t, usage)
	assert.Greater(t, usage.ReasoningTokens, 0)
	assert.Greater(t, usage.OutputTokens, usage.ReasoningTokens)
}

func TestUsageNormalizingStreamPassesProviderUsageThrough(t *testing.T) {
	inner := &eventListStream{events: []inference.StreamEvent{
		{
			Kind:   inference.StreamDelta,
			Deltas: []inference.ChoiceDelta{{Text: "measured", FinishReason: "stop"}},
		},
		{
			Kind:  inference.StreamUsage,
			Usage: &inference.Usage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18},
		},
	}}
	stream := newUsageNormalizingStream(inner, promptMessages(), tokenize.NewEstimator())

	events := drainStream(t, stream)
	require.Len(t, events, 2)
	var usageEvents []*inference.StreamEvent
	for _, event := range events {
		if event.Usage != nil {
			usageEvents = append(usageEvents, event)
		}
	}
	require.Len(t, usageEvents, 1)
	assert.False(t, usageEvents[0].Usage.Estimated)
	assert.Equal(t, 11, usageEvents[0].Usage.InputTokens)
}

func TestUsageNormalizingStreamQueuesSynthesisAfterFinalEventWithEOF(t *testing.T) {
	inner := &eventListStream{
		events: []inference.StreamEvent{
			{
				Kind:   inference.StreamDelta,
				ID:     "chatcmpl-2",
				Deltas: []inference.ChoiceDelta{{Text: "tail", FinishReason: "stop"}},
			},
		},
		finalWithEOF: true,
	}
	stream := newUsageNormalizingStream(inner, promptMessages(), tokenize.NewEstimator())

	first, err := stream.Read()
	require.NoError(t, err)
	require.Equal(t, inference.StreamDelta, first.Kind)

	second, err := stream.Read()
	require.NoError(t, err)
	require.Equal(t, inference.StreamUsage, second.Kind)
	require.NotNil(t, second.Usage)
	assert.True(t, second.Usage.Estimated)
	assert.Equal(t, "chatcmpl-2", second.ID)

	_, err = stream.Read()
	require.Equal(t, io.EOF, err)
}

func TestUsageNormalizingStreamForwardsCloseAndUnwrap(t *testing.T) {
	inner := &eventListStream{}
	stream := newUsageNormalizingStream(inner, nil, tokenize.NewEstimator())
	require.NoError(t, stream.Close())
	assert.True(t, inner.closed)
	unwrapper, ok := stream.(StreamUnwrapper)
	require.True(t, ok)
	assert.Same(t, ChatCompletionStreamResponse(inner), unwrapper.Unwrap())
}

func TestNewUsageNormalizingStreamWithoutEstimatorReturnsStream(t *testing.T) {
	inner := &eventListStream{}
	assert.Same(t, ChatCompletionStreamResponse(inner), newUsageNormalizingStream(inner, nil, nil))
}

func TestProcessChatCompletionStreamForcesProviderUsage(t *testing.T) {
	router := &streamCapturingRouter{}
	service := &proxy{router: router, estimator: tokenize.NewEstimator()}

	stream, err := service.ProcessChatCompletionStream(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model:  "anthropic/claude-3",
			Stream: true,
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
			}},
		},
	})
	require.NoError(t, err)
	defer stream.Close()

	require.NotNil(t, router.received.StreamOptions)
	assert.True(t, router.received.StreamOptions.IncludeUsage)

	// The routed stream emits no usage, so the wrapper synthesizes one.
	events := drainStream(t, stream)
	require.NotEmpty(t, events)
	last := events[len(events)-1]
	require.Equal(t, inference.StreamUsage, last.Kind)
	require.NotNil(t, last.Usage)
	assert.True(t, last.Usage.Estimated)
}
