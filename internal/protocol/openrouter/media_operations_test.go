package openrouter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
)

// OpenRouter publishes three media paths against the same request bodies the
// OpenAI API uses. Its answers differ in one way that runs through every
// OpenRouter response: they name the model that served the call and the
// provider behind it.

// TestEncodeImagesHoldsTheOpenRouterShape pins the image answer, including the
// two fields that separate it from the OpenAI one.
func TestEncodeImagesHoldsTheOpenRouterShape(t *testing.T) {
	encoded, err := json.Marshal(EncodeImages(inference.ImagesResponse{
		Model:       "openai/gpt-image-1",
		CreatedUnix: 1767225600,
		Images: []inference.GeneratedImage{
			{B64JSON: "aGVsbG8=", RevisedPrompt: "a red bicycle, studio lit"},
		},
		Usage: inference.Usage{InputTokens: 12, TotalTokens: 12, GeneratedImages: 1},
	}))
	require.NoError(t, err)

	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, float64(1767225600), wire["created"])
	require.Equal(t, "openai/gpt-image-1", wire["model"])
	require.Equal(t, "openai", wire["provider"])

	data, ok := wire["data"].([]any)
	require.True(t, ok, string(encoded))
	require.Len(t, data, 1)
	first := data[0].(map[string]any)
	require.Equal(t, "aGVsbG8=", first["b64_json"])
	require.Equal(t, "a red bicycle, studio lit", first["revised_prompt"])

	usage, ok := wire["usage"].(map[string]any)
	require.True(t, ok, string(encoded))
	require.Equal(t, float64(12), usage["total_tokens"])
}

// TestEncodeTranscriptionHoldsTheOpenRouterShape pins the transcript answer.
// A model with no provider prefix leaves the provider field out rather than
// guessing at one.
func TestEncodeTranscriptionHoldsTheOpenRouterShape(t *testing.T) {
	encoded, err := json.Marshal(EncodeTranscription(inference.TranscriptionResponse{
		Model: "openai/whisper-1", Text: "the recorded sentence",
		Language: "en", Duration: 4.25,
	}))
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, "the recorded sentence", wire["text"])
	require.Equal(t, "openai/whisper-1", wire["model"])
	require.Equal(t, "openai", wire["provider"])
	require.Equal(t, "en", wire["language"])

	bare, err := json.Marshal(EncodeTranscription(inference.TranscriptionResponse{Text: "hello"}))
	require.NoError(t, err)
	require.JSONEq(t, `{"text":"hello"}`, string(bare))
}

// TestDecodeImagesRefusesAnUnknownField holds the strict-decode rule the other
// OpenRouter request bodies follow.
func TestDecodeImagesRefusesAnUnknownField(t *testing.T) {
	request, err := DecodeImages(strings.NewReader(
		`{"model":"openai/gpt-image-1","prompt":"a red bicycle","n":2,"size":"1024x1024"}`))
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-image-1", request.Model)
	require.Equal(t, "a red bicycle", request.Prompt)
	require.Equal(t, 2, request.N)
	require.False(t, request.IsEdit())

	_, err = DecodeImages(strings.NewReader(`{"model":"m","prompt":"p","widht":"1024"}`))
	require.Error(t, err)
}
