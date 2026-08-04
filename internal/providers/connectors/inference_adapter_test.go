package connectors

import (
	"encoding/json"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestInferenceAdapterPreservesChatSemantics(t *testing.T) {
	maxTokens := 200
	original := inference.ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []inference.Message{{
			Role: inference.RoleUser,
			Content: []inference.ContentPart{
				{Kind: inference.ContentText, Text: "inspect"},
				{Kind: inference.ContentImage, Image: &inference.Image{URL: "https://example.invalid/image.png", Detail: "high"}},
			},
		}},
		Tools:      []inference.Tool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: inference.ToolChoice{Mode: inference.ToolChoiceNamed, Name: "lookup"},
		Output: inference.StructuredOutput{
			Format: inference.OutputJSONSchema, Name: "result",
			Schema: json.RawMessage(`{"type":"object","required":["id"]}`), Strict: true,
		},
		Reasoning: inference.Reasoning{Effort: inference.ReasoningHigh, MaxTokens: &maxTokens},
		Stream:    true, StreamOptions: inference.StreamOptions{IncludeUsage: true},
	}

	wire, err := ChatRequestFromInference(original)
	require.NoError(t, err)
	roundTrip, err := ChatRequestToInference(wire)
	require.NoError(t, err)
	require.Equal(t, original, roundTrip)
}

func TestStreamEventsToInferenceSeparatesUsage(t *testing.T) {
	usage := Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}
	events, err := StreamEventsToInference(&ChatStreamChunk{
		ID: "chunk-1", Model: "gpt-4.1",
		Choices: []StreamChoice{{Index: 0, Delta: MessageDelta{Content: "hello"}, FinishReason: "stop"}},
		Usage:   &usage,
	}, "openai/gpt-4.1")
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, inference.StreamDelta, events[0].Kind)
	require.Equal(t, "hello", events[0].Deltas[0].Text)
	require.Equal(t, inference.StreamUsage, events[1].Kind)
	require.Equal(t, 6, events[1].Usage.TotalTokens)
}
