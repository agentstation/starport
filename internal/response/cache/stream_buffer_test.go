package cache

import (
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestStreamBufferLimitsNestedPayloads(t *testing.T) {
	large := strings.Repeat("x", StreamCacheByteLimit)
	for _, tc := range []struct {
		name  string
		delta inference.ChoiceDelta
	}{
		{"text", inference.ChoiceDelta{Text: large}},
		{"reasoning", inference.ChoiceDelta{Reasoning: large}},
		{"tool", inference.ChoiceDelta{ToolCalls: []inference.ToolCall{{Arguments: large}}}},
		{"audio", inference.ChoiceDelta{Audio: &inference.AudioChunk{Data: []byte(large)}}},
		{"logprobs", inference.ChoiceDelta{LogProbs: []inference.LogProb{{Top: []inference.TopLogProb{{Token: large}}}}}},
		{"image", inference.ChoiceDelta{Media: []inference.ContentPart{{Image: &inference.Image{URL: large}}}}},
		{"document", inference.ChoiceDelta{Media: []inference.ContentPart{{Document: &inference.Document{Data: []byte(large)}}}}},
		{"video", inference.ChoiceDelta{Media: []inference.ContentPart{{Video: &inference.Video{Data: []byte(large)}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b StreamBuffer
			b.Add(inference.StreamEvent{Kind: inference.StreamStart})
			require.NotEmpty(t, b.Events())
			b.Add(inference.StreamEvent{Deltas: []inference.ChoiceDelta{tc.delta}})
			require.Empty(t, b.Events())
			require.Zero(t, b.RetainedBytes())
			b.Add(inference.StreamEvent{Kind: inference.StreamEnd})
			require.Empty(t, b.Events(), "a discarded stream cannot resume caching")
		})
	}
}

func TestStreamBufferOwnsRetainedData(t *testing.T) {
	source := inference.StreamEvent{Kind: inference.StreamDelta, Usage: &inference.Usage{OutputTokens: 1}, Deltas: []inference.ChoiceDelta{{
		Text: "answer", ToolCalls: []inference.ToolCall{{Arguments: "{}"}},
		Audio:    &inference.AudioChunk{Data: []byte("audio"), Transcript: "spoken"},
		LogProbs: []inference.LogProb{{Token: "t", Bytes: []int{1}, Top: []inference.TopLogProb{{Token: "a", Bytes: []int{2}}}}},
		Media: []inference.ContentPart{
			{Image: &inference.Image{URL: "image"}},
			{Audio: &inference.Audio{Data: []byte("sound")}},
			{Document: &inference.Document{Data: []byte("document")}},
			{Video: &inference.Video{Data: []byte("video")}},
		},
	}}}
	expected := source.Clone()
	var b StreamBuffer
	b.Add(source)
	require.Equal(t, expected, b.Events()[0])
	require.Positive(t, b.RetainedBytes())
	require.LessOrEqual(t, b.RetainedBytes(), StreamCacheByteLimit)
	source.Usage.OutputTokens = 9
	d := &source.Deltas[0]
	d.Text = "changed"
	d.ToolCalls[0].Arguments = "changed"
	d.Audio.Data[0] = 'X'
	d.LogProbs[0].Bytes[0] = 9
	d.LogProbs[0].Top[0].Bytes[0] = 9
	d.Media[0].Image.URL = "changed"
	d.Media[1].Audio.Data[0] = 'X'
	d.Media[2].Document.Data[0] = 'X'
	d.Media[3].Video.Data[0] = 'X'
	require.Equal(t, expected, b.Events()[0])
	b.Discard()
	require.Empty(t, b.Events())
	require.Zero(t, b.RetainedBytes())
}

func TestStreamBufferStopsEmptyEventRetention(t *testing.T) {
	var b StreamBuffer
	for range StreamCacheEventLimit + 1 {
		b.Add(inference.StreamEvent{})
		require.LessOrEqual(t, b.RetainedBytes(), StreamCacheByteLimit)
		require.LessOrEqual(t, len(b.Events()), StreamCacheEventLimit)
	}
	require.Empty(t, b.Events())
}

func BenchmarkStreamBufferEvent(b *testing.B) {
	event := inference.StreamEvent{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{Text: strings.Repeat("x", 64)}}}
	var buffer StreamBuffer
	b.ReportAllocs()
	for b.Loop() {
		if buffer.disabled {
			buffer = StreamBuffer{}
		}
		buffer.Add(event)
	}
}
