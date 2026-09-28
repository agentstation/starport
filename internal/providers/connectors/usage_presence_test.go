package connectors

import (
	"encoding/json/v2"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageTotalsPreservePresence(t *testing.T) {
	for _, test := range []struct {
		body  string
		known bool
	}{
		{`{}`, false},
		{`null`, false},
		{`{"completion_tokens":3,"total_tokens":3}`, false},
		{`{"prompt_tokens":null,"completion_tokens":3,"total_tokens":3}`, false},
		{`{"prompt_tokens":8,"total_tokens":8}`, false},
		{`{"prompt_tokens":0,"completion_tokens":0}`, false},
		{`{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, true},
		{`{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}`, true},
	} {
		t.Run(test.body, func(t *testing.T) {
			var usage Usage
			require.NoError(t, json.Unmarshal([]byte(test.body), &usage))
			require.Equal(t, test.known, usage.HasReportedTotals())
			retained := usage.Copy()
			require.Equal(t, test.known, retained.HasReportedTotals())
			data, err := json.Marshal(retained)
			require.NoError(t, err)
			var decoded Usage
			require.NoError(t, json.Unmarshal(data, &decoded))
			require.Equal(t, test.known, decoded.HasReportedTotals())
			require.Equal(t, usage, decoded)
		})
	}
	var response ChatResponse
	require.NoError(t, json.Unmarshal([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), &response))
	require.NotNil(t, response.ReportedUsage())
	require.True(t, response.ReportedUsage().HasReportedTotals())
}

func TestNativeUsagePreservesMissingTotals(t *testing.T) {
	for _, test := range []struct {
		name, body string
		known      bool
	}{
		{"anthropic missing input", `{"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`, false},
		{"anthropic missing cache", `{"input_tokens":8,"output_tokens":3}`, false},
		{"anthropic complete", `{"input_tokens":8,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`, true},
		{"anthropic zero", `{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var raw anthropicUsage
			require.NoError(t, json.Unmarshal([]byte(test.body), &raw))
			require.Equal(t, test.known, convertAnthropicUsage(raw).HasReportedTotals())
		})
	}
	for _, test := range []struct {
		body  string
		known bool
	}{
		{`{"candidatesTokenCount":3,"totalTokenCount":3}`, false},
		{`{"promptTokenCount":8,"candidatesTokenCount":3}`, false},
		{`{"promptTokenCount":8,"candidatesTokenCount":3,"totalTokenCount":11}`, true},
		{`{"promptTokenCount":0,"totalTokenCount":0}`, true},
	} {
		t.Run(test.body, func(t *testing.T) {
			var raw geminiUsageMetadata
			require.NoError(t, json.Unmarshal([]byte(test.body), &raw))
			require.Equal(t, test.known, convertGeminiUsage(raw).HasReportedTotals())
		})
	}
}

func TestOllamaUsagePreservesMissingTotals(t *testing.T) {
	for _, test := range []struct {
		counts string
		known  bool
	}{
		{`"eval_count":3`, false},
		{`"prompt_eval_count":8`, false},
		{`"prompt_eval_count":null,"eval_count":3`, false},
		{`"prompt_eval_count":0,"eval_count":0`, true},
		{`"prompt_eval_count":8,"eval_count":3`, true},
	} {
		t.Run(test.counts, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"model":"model","message":{"role":"assistant","content":"hello"},`+test.counts+`}`)
			}))
			defer server.Close()
			connector, err := NewOllamaConnector(ProviderConfig{BaseURL: server.URL, Timeout: time.Second, Enabled: true})
			require.NoError(t, err)
			defer connector.Close()
			request := approveConnectorFixture(t, &ChatRequest{Model: "model", Credential: testAPIMaterial("fixture"), Endpoint: InferenceEndpoint{Type: "ollama", URL: server.URL + "/api/chat"}, Messages: []Message{{Role: "user", Content: "hello"}}})
			response, err := connector.Chat(t.Context(), request)
			require.NoError(t, err)
			require.Equal(t, test.known, response.Usage.HasReportedTotals())
		})
	}
}

func TestNativeUsageRejectsInvalidComponents(t *testing.T) {
	for _, raw := range []anthropicUsage{
		{InputTokens: math.MaxInt, CacheReadInputTokens: math.MaxInt, CacheCreationInputTokens: 3, OutputTokens: 1},
		{InputTokens: 8, CacheReadInputTokens: -1, OutputTokens: 3},
		{InputTokens: 8, OutputTokens: -1},
	} {
		require.False(t, convertAnthropicUsage(raw).HasReportedTotals())
	}
	for _, raw := range []geminiUsageMetadata{
		{PromptTokenCount: 8, CandidatesTokenCount: -1, ThoughtsTokenCount: 3, TotalTokenCount: 10},
		{CandidatesTokenCount: math.MaxInt, ThoughtsTokenCount: 1},
		{PromptTokenCount: 8, CachedContentTokenCount: -1, TotalTokenCount: 8},
	} {
		require.False(t, convertGeminiUsage(raw).HasReportedTotals())
	}
}
