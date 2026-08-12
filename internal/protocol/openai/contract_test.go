package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func TestOpenAIProtocolContract(t *testing.T) {
	t.Run("chat request", func(t *testing.T) {
		request, err := DecodeChat(strings.NewReader(`{
			"model":"openai/gpt-4.1",
			"messages":[
				{"role":"system","content":"Be exact."},
				{"role":"user","content":[{"type":"text","text":"Inspect"},{"type":"image_url","image_url":{"url":"https://example.test/image.png","detail":"high"}}]}
			],
			"max_completion_tokens":128,
			"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],
			"tool_choice":{"type":"function","function":{"name":"lookup"}},
			"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":{"type":"object"},"strict":true}},
			"reasoning_effort":"high",
			"logprobs":true,"top_logprobs":3,
			"stream":true,"stream_options":{"include_usage":true}
		}`))
		require.NoError(t, err)
		require.Equal(t, "openai/gpt-4.1", request.Model)
		require.Equal(t, "https://example.test/image.png", request.Messages[1].Content[1].Image.URL)
		require.Equal(t, 128, *request.Sampling.MaxTokens)
		require.Equal(t, inference.ToolChoiceNamed, request.ToolChoice.Mode)
		require.Equal(t, inference.OutputJSONSchema, request.Output.Format)
		require.Equal(t, inference.ReasoningHigh, request.Reasoning.Effort)
		require.True(t, request.StreamOptions.IncludeUsage)
		require.JSONEq(t, `true`, string(request.Extensions["logprobs"]))
		require.JSONEq(t, `3`, string(request.Extensions["top_logprobs"]))
	})

	t.Run("chat response", func(t *testing.T) {
		encoded, err := json.Marshal(EncodeChat(contractResponse()))
		require.NoError(t, err)
		require.JSONEq(t, `{
			"id":"chatcmpl-test","object":"chat.completion","created":1744329600,"model":"openai/gpt-4.1",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"stop","logprobs":{"content":[{"token":"done","logprob":-0.1,"top_logprobs":[{"token":"done","logprob":-0.1}]}]}}],
			"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"completion_tokens_details":{"reasoning_tokens":2}}
		}`, string(encoded))
	})

	t.Run("stream and error", func(t *testing.T) {
		finish := "stop"
		event := inference.StreamEvent{
			Kind: inference.StreamEnd, ID: "chatcmpl-test", CreatedUnix: 1744329600,
			Model: "openai/gpt-4.1", Deltas: []inference.ChoiceDelta{{
				Index: 0, FinishReason: finish,
				LogProbs: []inference.LogProb{{Token: "done", Value: -0.1}},
			}},
		}
		encoded, err := json.Marshal(EncodeStream(event))
		require.NoError(t, err)
		require.JSONEq(t, `{"id":"chatcmpl-test","object":"chat.completion.chunk","created":1744329600,"model":"openai/gpt-4.1","choices":[{"index":0,"delta":{},"finish_reason":"stop","logprobs":{"content":[{"token":"done","logprob":-0.1,"top_logprobs":[]}]}}]}`, string(encoded))

		usage := inference.Usage{InputTokens: 8, OutputTokens: 3, TotalTokens: 11}
		usageOnly, err := json.Marshal(EncodeStream(inference.StreamEvent{
			Kind: inference.StreamUsage, ID: "chatcmpl-test", Model: "openai/gpt-4.1", Usage: &usage,
		}))
		require.NoError(t, err)
		require.Contains(t, string(usageOnly), `"choices":[]`)

		recorder := httptest.NewRecorder()
		param := "model"
		WriteError(recorder, http.StatusBadRequest, "invalid_request_error", "model is required", &param)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.JSONEq(t, `{"error":{"message":"model is required","type":"invalid_request_error","param":"model"}}`, recorder.Body.String())
	})

	t.Run("embedding", func(t *testing.T) {
		request, err := DecodeEmbedding(strings.NewReader(`{"model":"openai/text-embedding-3-small","input":[[1,2,3]],"dimensions":256}`))
		require.NoError(t, err)
		require.Equal(t, [][]int{{1, 2, 3}}, request.Input.TokenIDs)
		encoded, err := json.Marshal(EncodeEmbedding(inference.EmbeddingResponse{
			Model: request.Model, Data: []inference.Embedding{{Index: 0, Vector: []float32{0.1, 0.2}}},
			Usage: inference.Usage{InputTokens: 3, TotalTokens: 3},
		}))
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"object":"list"`)
	})
}

func contractResponse() inference.ChatResponse {
	return inference.ChatResponse{
		ID: "chatcmpl-test", CreatedUnix: 1744329600, Model: "openai/gpt-4.1", ModelUsed: "openai/gpt-4.1",
		Choices: []inference.Choice{{
			Index: 0, FinishReason: "stop",
			LogProbs: []inference.LogProb{{
				Token: "done", Value: -0.1,
				Top: []inference.TopLogProb{{Token: "done", Value: -0.1}},
			}},
			Message: inference.Message{
				Role:      inference.RoleAssistant,
				Content:   []inference.ContentPart{{Kind: inference.ContentText, Text: "done"}},
				ToolCalls: []inference.ToolCall{{ID: "call_1", Name: "lookup", Arguments: `{}`}},
			},
		}},
		Usage: inference.Usage{InputTokens: 8, OutputTokens: 3, TotalTokens: 11, ReasoningTokens: 2},
	}
}
