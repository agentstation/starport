package connectors

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVertexEmbeddingsInputsAndMeasurements(t *testing.T) {
	for _, test := range []struct {
		name       string
		input      any
		statistics string
		count      int
		tokens     int64
		known      bool
	}{
		{"text measured", "hello world", `{"token_count":13,"truncated":false}`, 1, 13, true},
		{"text batch", []string{"hello", "world"}, `{"token_count":13,"truncated":false}`, 2, 26, true},
		{"absent statistics", "hello world", `null`, 1, 0, false},
		{"missing token count", "hello world", `{"truncated":false}`, 1, 0, false},
		{"null token count", "hello world", `{"token_count":null}`, 1, 0, false},
		{"explicit zero", "hello world", `{"token_count":0,"truncated":false}`, 1, 0, true},
		{"negative token count", "hello world", `{"token_count":-1}`, 1, 0, false},
		{"overflow", []string{"hello", "world"}, `{"token_count":` + strconv.Itoa(math.MaxInt) + `}`, 2, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Instances []struct {
						Content string `json:"content"`
					} `json:"instances"`
					Parameters struct {
						Dimensions   int   `json:"outputDimensionality"`
						AutoTruncate *bool `json:"autoTruncate"`
					} `json:"parameters"`
				}
				require.NoError(t, json.UnmarshalRead(r.Body, &request))
				require.Len(t, request.Instances, test.count)
				assert.Equal(t, 2, request.Parameters.Dimensions)
				require.NotNil(t, request.Parameters.AutoTruncate)
				require.False(t, *request.Parameters.AutoTruncate)
				predictions := make([]jsontext.Value, test.count)
				for i := range predictions {
					predictions[i] = jsontext.Value(`{"embeddings":{"values":[0.1,0.2],"statistics":` + test.statistics + `}}`)
				}
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.MarshalWrite(w, map[string]any{"predictions": predictions}))
			}))
			defer server.Close()
			connector, err := NewVertexAIConnector(ProviderConfig{BaseURL: server.URL, Timeout: time.Second})
			require.NoError(t, err)
			defer connector.Close()
			request := approveConnectorFixture(t, &EmbeddingsRequest{Model: "opaque-model", Input: test.input, Dimensions: new(2), Credential: testGoogleDefaultMaterial("fixture"), Endpoint: InferenceEndpoint{Type: "google-cloud", URL: server.URL + "/predict"}})
			var response *EmbeddingsResponse
			require.NotPanics(t, func() { response, err = connector.Embeddings(t.Context(), request) })
			require.NoError(t, err)
			require.NotNil(t, response)
			require.Len(t, response.Data, test.count)
			for i, item := range response.Data {
				require.Equal(t, i, item.Index)
				require.Equal(t, []float32{0.1, 0.2}, item.Embedding)
			}
			tokens, known := response.ReportedInputTokens()
			require.Equal(t, test.known, known)
			require.Equal(t, test.tokens, tokens)
			if !test.known {
				require.Zero(t, response.Usage.PromptTokens)
				require.Zero(t, response.Usage.TotalTokens)
			}
		})
	}
}

func TestVertexEmbeddingsRejectInvalidInputBeforeHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("invalid input reached provider")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	connector, err := NewVertexAIConnector(ProviderConfig{BaseURL: server.URL, Timeout: time.Second})
	require.NoError(t, err)
	defer connector.Close()
	for _, input := range []any{nil, "", []string{}, []string{"okay", ""}, []int{1, 2}, [][]int{{1, 2}}, map[string]any{"text": "hello"}} {
		request := approveConnectorFixture(t, &EmbeddingsRequest{Model: "model", Input: input, Credential: testGoogleDefaultMaterial("fixture"), Endpoint: InferenceEndpoint{Type: "google-cloud", URL: server.URL + "/predict"}})
		require.NotPanics(t, func() { _, err = connector.Embeddings(t.Context(), request) })
		require.Error(t, err)
	}
}

func TestVertexEmbeddingResponseIntegrity(t *testing.T) {
	for _, test := range []struct {
		name        string
		predictions string
		count       int
		known       bool
		wantError   bool
		tokens      int64
	}{
		{"missing prediction", `[{"embeddings":{"values":[0.1,0.2],"statistics":{"token_count":4}}}]`, 2, false, true, 0},
		{"extra prediction", `[{"embeddings":{"values":[0.1,0.2],"statistics":{"token_count":4}}},{"embeddings":{"values":[0.1,0.2],"statistics":{"token_count":5}}}]`, 1, false, true, 0},
		{"partial statistics", `[{"embeddings":{"values":[0.1,0.2],"statistics":{"token_count":4}}},{"embeddings":{"values":[0.1,0.2]}}]`, 2, false, false, 0},
		{"truncated input", `[{"embeddings":{"values":[0.1,0.2],"statistics":{"token_count":4,"truncated":true}}}]`, 1, false, true, 0},
		{"wrong dimensions with paid usage", `[{"embeddings":{"values":[0.1],"statistics":{"token_count":4}}}]`, 1, true, true, 4},
		{"empty vector with paid usage", `[{"embeddings":{"values":[],"statistics":{"token_count":4}}}]`, 1, true, true, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			var wire vertexEmbeddingResponse
			require.NoError(t, json.Unmarshal([]byte(`{"predictions":`+test.predictions+`}`), &wire))
			response, err := wire.normalized(&EmbeddingsRequest{Model: "model", Dimensions: new(2)}, test.count)
			require.Equal(t, test.wantError, err != nil)
			require.NotNil(t, response)
			tokens, known := response.ReportedInputTokens()
			require.Equal(t, test.known, known)
			require.Equal(t, test.tokens, tokens)
		})
	}
}
