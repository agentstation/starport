package openrouter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestOpenRouterProtocolContract(t *testing.T) {
	t.Run("routed chat request", func(t *testing.T) {
		request, err := DecodeChat(strings.NewReader(`{
			"models":["openai/gpt-4.1","anthropic/claude-sonnet-4"],
			"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}],
			"provider":{"order":["openai","anthropic"],"only":["openai","anthropic"],"allow_fallbacks":true,"require_parameters":true},
			"route":"fallback","reasoning":{"effort":"medium","max_tokens":512,"exclude":true},
			"top_k":40,"transforms":["middle-out"],
			"response_format":{"type":"json_object"}
		}`))
		require.NoError(t, err)
		require.Equal(t, []string{"openai/gpt-4.1", "anthropic/claude-sonnet-4"}, request.Inference.FallbackModels)
		require.Equal(t, "ephemeral", request.Inference.Messages[0].Content[0].CacheControl)
		require.Equal(t, inference.ReasoningMedium, request.Inference.Reasoning.Effort)
		require.Equal(t, 512, *request.Inference.Reasoning.MaxTokens)
		require.True(t, request.Inference.Reasoning.Exclude)
		require.Equal(t, inference.OutputJSONObject, request.Inference.Output.Format)
		require.Equal(t, "fallback", request.Route)
		require.Equal(t, []string{"openai", "anthropic"}, request.Provider.Order)
		require.True(t, *request.Provider.AllowFallbacks)
		require.JSONEq(t, `40`, string(request.Inference.Extensions["top_k"]))

		request, err = DecodeChat(strings.NewReader(`{
			"model":"openai/o3","messages":[],"reasoning_effort":"high","reasoning":{"max_tokens":512}
		}`))
		require.NoError(t, err)
		require.Equal(t, inference.ReasoningHigh, request.Inference.Reasoning.Effort)
	})

	t.Run("response and midstream error", func(t *testing.T) {
		response := inference.ChatResponse{
			ID: "chatcmpl-test", CreatedUnix: 1744329600, Model: "openai/gpt-4.1", ModelUsed: "openai/gpt-4.1",
			Choices: []inference.Choice{{
				Index: 0, FinishReason: "stop",
				Message: inference.Message{Role: inference.RoleAssistant, Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "done"}}, Reasoning: "checked"},
			}},
			Usage: inference.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
		}
		encoded, err := json.Marshal(EncodeChat(response))
		require.NoError(t, err)
		require.JSONEq(t, `{
			"id":"chatcmpl-test","object":"chat.completion","created":1744329600,"model":"openai/gpt-4.1","provider":"openai",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done","reasoning":"checked"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6},"system_fingerprint":null
		}`, string(encoded))

		usage := inference.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6}
		usageOnly, err := json.Marshal(EncodeStream(inference.StreamEvent{
			Kind: inference.StreamUsage, ID: "chatcmpl-test", Model: "openai/gpt-4.1", Usage: &usage,
		}))
		require.NoError(t, err)
		require.Contains(t, string(usageOnly), `"choices":[]`)
		require.Contains(t, string(usageOnly), `"system_fingerprint":null`)

		errorChunk, err := json.Marshal(EncodeStreamError(inference.StreamEvent{ID: "chatcmpl-test", Model: "openai/gpt-4.1"}, 502, "Provider stream failed", map[string]any{"error_type": "provider_error"}))
		require.NoError(t, err)
		require.JSONEq(t, `{
			"id":"chatcmpl-test","object":"chat.completion.chunk","model":"openai/gpt-4.1",
			"error":{"code":502,"message":"Provider stream failed","metadata":{"error_type":"provider_error"}},
			"choices":[{"index":0,"delta":{},"finish_reason":"error"}]
		}`, string(errorChunk))
	})

	t.Run("model list and numeric error", func(t *testing.T) {
		modelList := ModelList{
			Data: []Model{{
				ID: "openai/gpt-4.1", CanonicalSlug: "openai/gpt-4.1", Name: "GPT-4.1",
				Created: 1744329600, ContextLength: 128000,
				Pricing:             &Pricing{Prompt: "0.000002", Completion: "0.000008"},
				SupportedParameters: []string{"tools"},
			}},
			TotalCount: 1, Links: ModelLinks{Next: nil},
		}
		encoded, err := json.Marshal(modelList)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"total_count":1`)
		require.Contains(t, string(encoded), `"links":{"next":null}`)

		recorder := httptest.NewRecorder()
		WriteError(recorder, http.StatusTooManyRequests, "Rate limit exceeded", map[string]any{"error_type": "rate_limit_error"})
		require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		require.JSONEq(t, `{"error":{"code":429,"message":"Rate limit exceeded","metadata":{"error_type":"rate_limit_error"}}}`, recorder.Body.String())
	})

	t.Run("embedding", func(t *testing.T) {
		request, err := DecodeEmbedding(strings.NewReader(`{"model":"openai/text-embedding-3-small","input":["one","two"]}`))
		require.NoError(t, err)
		require.Equal(t, []string{"one", "two"}, request.Input.Texts)
		response := EncodeEmbedding(inference.EmbeddingResponse{Model: request.Model})
		require.Equal(t, "openai", response.Provider)
	})
}
