package connectors

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEmbeddingUsagePresence(t *testing.T) {
	for _, test := range []struct {
		wire   string
		known  bool
		tokens int64
	}{
		{`{}`, false, 0}, {`{"usage":null}`, false, 0}, {`{"usage":{}}`, false, 0},
		{`{"usage":{"prompt_tokens":null,"total_tokens":8}}`, false, 0},
		{`{"usage":{"prompt_tokens":8}}`, false, 0},
		{`{"usage":{"total_tokens":8}}`, false, 0},
		{`{"usage":{"prompt_tokens":8,"total_tokens":9}}`, false, 0},
		{`{"usage":{"prompt_tokens":-1,"total_tokens":-1}}`, false, 0},
		{`{"usage":{"prompt_tokens":8,"total_tokens":8,"completion_tokens":1}}`, false, 0},
		{`{"usage":{"prompt_tokens":8,"total_tokens":8}}`, true, 8},
		{`{"usage":{"prompt_tokens":0,"total_tokens":0}}`, true, 0},
	} {
		t.Run(test.wire, func(t *testing.T) {
			var response EmbeddingsResponse
			require.NoError(t, json.Unmarshal([]byte(test.wire), &response))
			for range 2 {
				tokens, known := response.ReportedInputTokens()
				require.Equal(t, test.known, known)
				require.Equal(t, test.tokens, tokens)
				encoded, err := json.Marshal(response)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(encoded, &response))
			}
		})
	}
	for _, response := range []*EmbeddingsResponse{nil, {}, {Usage: Usage{PromptTokens: 8, TotalTokens: 8, decoded: true}}} {
		_, known := response.ReportedInputTokens()
		require.False(t, known)
	}
}

func TestNativeEmbeddingUsageRemainsUnknown(t *testing.T) {
	for _, kind := range []string{"google", "vertex", "ollama"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := `{"embedding":{"values":[0.1,0.2]}}`
				if kind == "vertex" {
					body = `{"predictions":[{"embeddings":{"values":[0.1,0.2]}}]}`
				}
				if kind == "ollama" {
					body = `{"embedding":[0.1,0.2]}`
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			cfg := ProviderConfig{BaseURL: server.URL, Timeout: time.Second, Enabled: true}
			var connector Connector
			var err error
			request := &EmbeddingsRequest{Model: "model", Input: "hello world", Endpoint: InferenceEndpoint{URL: server.URL + "/embed"}}
			switch kind {
			case "google":
				connector, err = NewGoogleAIStudioConnector(cfg)
				request.Credential = testGoogleMaterial("fixture")
				request.Endpoint.Type = "google"
			case "vertex":
				connector, err = NewVertexAIConnector(cfg)
				request.Credential = testGoogleDefaultMaterial("fixture")
				request.Endpoint.Type = "google-cloud"
			case "ollama":
				connector, err = NewOllamaConnector(cfg)
				request.Credential = testAPIMaterial("fixture")
				request.Endpoint.Type = "ollama"
			}
			require.NoError(t, err)
			defer connector.Close()
			response, err := connector.Embeddings(t.Context(), approveConnectorFixture(t, request))
			require.NoError(t, err)
			_, known := response.ReportedInputTokens()
			require.False(t, known)
			data, err := json.Marshal(response)
			require.NoError(t, err)
			var copied EmbeddingsResponse
			require.NoError(t, json.Unmarshal(data, &copied))
			_, known = copied.ReportedInputTokens()
			require.False(t, known)
		})
	}
}
