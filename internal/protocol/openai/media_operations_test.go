package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
)

// The dedicated media operations reach this codec at their own paths. An SDK
// written against the OpenAI API reads the answer below without a translation
// layer, so the field names and their nesting are the contract, not an
// implementation detail.

// TestEncodeImagesHoldsTheOpenAIShape pins the image answer. The OpenAI API
// names the list `data`, carries an inline picture as `b64_json`, and reports
// the rendering time as `created`.
func TestEncodeImagesHoldsTheOpenAIShape(t *testing.T) {
	encoded, err := json.Marshal(EncodeImages(inference.ImagesResponse{
		Model:       "openai/gpt-image-1",
		CreatedUnix: 1767225600,
		Images: []inference.GeneratedImage{
			{B64JSON: "aGVsbG8=", RevisedPrompt: "a red bicycle, studio lit"},
			{URL: "https://example.test/second.png"},
		},
		Usage: inference.Usage{InputTokens: 12, TotalTokens: 12, GeneratedImages: 2},
	}))
	require.NoError(t, err)

	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, float64(1767225600), wire["created"])

	data, ok := wire["data"].([]any)
	require.True(t, ok, string(encoded))
	require.Len(t, data, 2)

	first := data[0].(map[string]any)
	require.Equal(t, "aGVsbG8=", first["b64_json"])
	require.Equal(t, "a red bicycle, studio lit", first["revised_prompt"])
	require.NotContains(t, first, "url")

	second := data[1].(map[string]any)
	require.Equal(t, "https://example.test/second.png", second["url"])
	require.NotContains(t, second, "b64_json")

	usage, ok := wire["usage"].(map[string]any)
	require.True(t, ok, string(encoded))
	require.Equal(t, float64(12), usage["total_tokens"])

	// The OpenAI image answer carries no model field, and adding one would
	// make a strict SDK reject the body.
	require.NotContains(t, wire, "model")
}

// TestEncodeTranscriptionHoldsTheOpenAIShape pins the transcript answer. The
// two optional fields stay out of the body when the provider reported none,
// because a zero duration is a claim the gateway cannot make.
func TestEncodeTranscriptionHoldsTheOpenAIShape(t *testing.T) {
	full, err := json.Marshal(EncodeTranscription(inference.TranscriptionResponse{
		Model: "openai/whisper-1", Text: "the recorded sentence",
		Language: "en", Duration: 4.25,
	}))
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(full, &wire))
	require.Equal(t, "the recorded sentence", wire["text"])
	require.Equal(t, "en", wire["language"])
	require.Equal(t, 4.25, wire["duration"])

	bare, err := json.Marshal(EncodeTranscription(inference.TranscriptionResponse{Text: "hello"}))
	require.NoError(t, err)
	require.JSONEq(t, `{"text":"hello"}`, string(bare))
}

// TestDecodeImagesRefusesAnUnknownField holds the strict-decode rule the other
// OpenAI request bodies follow. A caller that misspells a field learns it here
// rather than watching the gateway ignore the value.
func TestDecodeImagesRefusesAnUnknownField(t *testing.T) {
	request, err := DecodeImages(strings.NewReader(
		`{"model":"openai/gpt-image-1","prompt":"a red bicycle","n":2,"size":"1024x1024"}`))
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-image-1", request.Model)
	require.Equal(t, "a red bicycle", request.Prompt)
	require.Equal(t, 2, request.N)
	require.Equal(t, "1024x1024", request.Size)
	require.False(t, request.IsEdit())

	_, err = DecodeImages(strings.NewReader(`{"model":"m","prompt":"p","widht":"1024"}`))
	require.Error(t, err)
}
