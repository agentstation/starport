package proxy

import (
	"encoding/json"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

func TestTransformChatRequestUsesCanonicalInference(t *testing.T) {
	candidateCount := 2
	request := &ChatCompletionRequest{Request: inference.ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []inference.Message{{
			Role: inference.RoleUser,
			Content: []inference.ContentPart{
				{Kind: inference.ContentText, Text: "inspect"},
				{Kind: inference.ContentImage, Image: &inference.Image{URL: "https://example.invalid/image.png"}},
			},
		}},
		Sampling: inference.Sampling{CandidateCount: &candidateCount},
		Tools: []inference.Tool{{
			Name:       "lookup",
			Parameters: json.RawMessage(`{"type":"object"}`),
		}},
		Output: inference.StructuredOutput{
			Format: inference.OutputJSONSchema,
			Name:   "result",
			Schema: json.RawMessage(`{"type":"object"}`),
			Strict: true,
		},
		StreamOptions: inference.StreamOptions{IncludeUsage: true},
	}}

	converted, err := TransformChatRequest(request)
	require.NoError(t, err)
	require.Equal(t, candidateCount, *converted.N)
	require.True(t, converted.StreamOptions.IncludeUsage)
	require.Equal(t, "result", converted.ResponseFormat.JSONSchema.Name)
	require.JSONEq(t, `{"type":"object"}`, string(converted.ResponseFormat.JSONSchema.Schema))
	require.Len(t, converted.Tools, 1)
}

func TestTransformChatResponseReturnsCanonicalInference(t *testing.T) {
	response, err := TransformChatResponse(&connectors.ChatResponse{
		ID:      "chatcmpl-test",
		Created: 10,
		Model:   "openai/gpt-4.1",
		Choices: []connectors.Choice{{
			Index:   0,
			Message: connectors.Message{Role: "assistant", Content: "Done"},
		}},
	}, "openai/gpt-4.1")
	require.NoError(t, err)
	require.Equal(t, "chatcmpl-test", response.Response.ID)
	require.Equal(t, "openai/gpt-4.1", response.Response.ModelUsed)
	require.Equal(t, "Done", response.Response.Choices[0].Message.Content[0].Text)
}

func TestTransformEmbeddingsRequestUsesCanonicalInference(t *testing.T) {
	request := &EmbeddingsRequest{Request: inference.EmbeddingRequest{
		Model: "openai/text-embedding-3-small",
		Input: inference.EmbeddingInput{TokenIDs: [][]int{{1, 2}}},
	}}
	converted := TransformEmbeddingsRequest(request)
	require.Equal(t, [][]int{{1, 2}}, converted.Input)
}
