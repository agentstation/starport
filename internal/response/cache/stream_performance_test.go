package cache

import (
	"runtime"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func boundedCompletionEvents(t testing.TB) []inference.StreamEvent {
	t.Helper()
	var buffer StreamBuffer
	for range 64 {
		buffer.Add(inference.StreamEvent{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{
			Text: strings.Repeat("t", 256), Reasoning: strings.Repeat("r", 256),
			ToolCalls: []inference.ToolCall{{ID: "tool", Name: "call", Arguments: strings.Repeat("a", 256)}},
		}}})
	}
	require.Len(t, buffer.Events(), 64, "fixture must fit the production retention bound")
	return buffer.Events()
}

func TestStreamCompletionAllocationBound(t *testing.T) {
	events := boundedCompletionEvents(t)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	response, err := CompleteStream(events)
	runtime.ReadMemStats(&after)
	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	require.Equal(t, strings.Repeat("t", 64*256), response.Choices[0].Message.Content[0].Text)
	require.Equal(t, strings.Repeat("r", 64*256), response.Choices[0].Message.Reasoning)
	require.Equal(t, strings.Repeat("a", 64*256), response.Choices[0].Message.ToolCalls[0].Arguments)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(512<<10), "bounded completion must not repeatedly copy accumulated strings")
}

func BenchmarkBoundedStreamCompletion(b *testing.B) {
	events := boundedCompletionEvents(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response, err := CompleteStream(events)
		if err != nil || len(response.Choices) != 1 {
			b.Fatal("invalid completion", err)
		}
	}
}

func TestStreamCompletionKeepsInterleavedChoices(t *testing.T) {
	events := []inference.StreamEvent{
		{Deltas: []inference.ChoiceDelta{
			{Index: 2, Media: []inference.ContentPart{{Kind: inference.ContentText, Text: "prefix"}}, Reasoning: "why", ToolCalls: []inference.ToolCall{{ID: "a", Name: "first", Arguments: "{"}, {ID: "b", Name: "second", Arguments: "["}}},
			{Index: 0, Text: "other"},
		}},
		{Deltas: []inference.ChoiceDelta{
			{Index: 2, Text: " answer", Reasoning: " now", ToolCalls: []inference.ToolCall{{ID: "b", Arguments: "]"}, {ID: "a", Arguments: "}"}}},
			{Index: 0, Text: " choice", FinishReason: "stop"},
		}},
	}
	response, err := CompleteStream(events)
	require.NoError(t, err)
	require.Len(t, response.Choices, 2)
	require.Equal(t, 0, response.Choices[0].Index)
	require.Equal(t, "other choice", response.Choices[0].Message.Content[0].Text)
	require.Equal(t, "stop", response.Choices[0].FinishReason)
	require.Equal(t, 2, response.Choices[1].Index)
	require.Equal(t, "prefix answer", response.Choices[1].Message.Content[0].Text)
	require.Equal(t, "why now", response.Choices[1].Message.Reasoning)
	require.Equal(t, []inference.ToolCall{{ID: "a", Name: "first", Arguments: "{}"}, {ID: "b", Name: "second", Arguments: "[]"}}, response.Choices[1].Message.ToolCalls)
	require.Equal(t, "prefix", events[0].Deltas[0].Media[0].Text)
	require.Equal(t, "{", events[0].Deltas[0].ToolCalls[0].Arguments)
}
