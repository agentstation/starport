package proxy

import (
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestValidateChatCompletionRequest(t *testing.T) {
	temperature := float32(0.7)
	tests := []struct {
		name    string
		request *ChatCompletionRequest
		field   string
	}{
		{
			name: "valid canonical request",
			request: &ChatCompletionRequest{Request: inference.ChatRequest{
				Model: "anthropic/claude-sonnet-4",
				Messages: []inference.Message{{
					Role:    inference.RoleUser,
					Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "Hello"}},
				}},
				Sampling: inference.Sampling{Temperature: &temperature},
			}},
		},
		{name: "nil request", field: "request"},
		{
			name:    "missing model",
			request: &ChatCompletionRequest{Request: inference.ChatRequest{Messages: []inference.Message{{Role: inference.RoleUser}}}},
			field:   "model",
		},
		{
			name:    "missing messages",
			request: &ChatCompletionRequest{Request: inference.ChatRequest{Model: "openai/gpt-4.1"}},
			field:   "messages",
		},
		{
			name: "invalid cache control",
			request: &ChatCompletionRequest{Request: inference.ChatRequest{
				Model: "anthropic/claude-sonnet-4",
				Messages: []inference.Message{{
					Role:    inference.RoleUser,
					Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "Hello", CacheControl: "persistent"}},
				}},
			}},
			field: "messages[0].content[0].cache_control.type",
		},
		{
			name: "unsupported route",
			request: &ChatCompletionRequest{
				Request: inference.ChatRequest{
					Model:    "openai/gpt-4.1",
					Messages: []inference.Message{{Role: inference.RoleUser}},
				},
				Route: "balanced",
			},
			field: "route",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateChatCompletionRequest(test.request)
			if test.field == "" {
				require.NoError(t, err)
				return
			}
			var validationErr *ValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, test.field, validationErr.Field)
		})
	}
}

func TestValidateEmbeddingsRequest(t *testing.T) {
	dimensions := 1536
	valid := &EmbeddingsRequest{Request: inference.EmbeddingRequest{
		Model:      "openai/text-embedding-3-small",
		Input:      inference.EmbeddingInput{Texts: []string{"Hello"}},
		Dimensions: &dimensions,
	}}
	require.NoError(t, ValidateEmbeddingsRequest(valid))

	invalid := &EmbeddingsRequest{Request: inference.EmbeddingRequest{
		Model: "openai/text-embedding-3-small",
		Input: inference.EmbeddingInput{Texts: []string{"Hello"}, TokenIDs: [][]int{{1, 2}}},
	}}
	var validationErr *ValidationError
	require.ErrorAs(t, ValidateEmbeddingsRequest(invalid), &validationErr)
	require.Equal(t, "input", validationErr.Field)
}

func TestExtractProviderFromModelUsesExactProviderPrefix(t *testing.T) {
	provider, model := ExtractProviderFromModel("google/gemini-2.5-pro")
	require.Equal(t, "google", provider)
	require.Equal(t, "gemini-2.5-pro", model)
}
