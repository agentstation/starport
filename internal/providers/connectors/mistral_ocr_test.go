package connectors

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestMistralOCRWireContract(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/ocr", r.URL.Path)
		require.Equal(t, "Bearer ocr-key", r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, json.UnmarshalRead(r.Body, &body))
		require.Equal(t, map[string]any{
			"model": "exact/model:id", "document": map[string]any{"type": "document_url", "document_url": "data:application/pdf;base64,cGRm"},
			"pages": []any{float64(0), float64(1)}, "include_image_base64": false, "include_blocks": false,
		}, body)
		_, _ = w.Write([]byte(`{"pages":[{"index":1,"markdown":"Second"},{"index":0,"markdown":"First"}],"usage_info":{"pages_processed":2}}`))
	}))
	defer server.Close()
	registry, err := ProductionTransportRegistry()
	require.NoError(t, err)
	c, err := registry.NewProviderConnector("fixture-provider", []catalogs.EndpointType{catalogs.EndpointTypeMistralOCR}, mediaTestConfig(server.URL))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	reader, ok := DocumentRecognizerFor(c, catalogs.EndpointTypeMistralOCR)
	require.True(t, ok)
	response, err := reader.RecognizeDocument(t.Context(), approveConnectorFixture(t, &RecognitionRequest{
		Model: "exact/model:id", Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeMistralOCR, URL: server.URL + "/ocr"}, Credential: testAPIMaterial("ocr-key"),
		Document: UploadedFile{Filename: "x.pdf", MediaType: "application/pdf", Bytes: []byte("pdf")}, Pages: 2,
	}))
	require.NoError(t, err)
	require.Equal(t, []RecognizedPage{{Number: 1, Text: "First"}, {Number: 2, Text: "Second"}}, response.Pages)
	require.Equal(t, 2, *response.ProcessedPages)
	require.Nil(t, response.TokenEvidence)
	canonical, err := RecognitionResponseToInference(response)
	require.NoError(t, err)
	require.True(t, canonical.Usage.TokensUnknown)
	require.True(t, canonical.Usage.ProcessedPagesKnown)
	require.Equal(t, 2, canonical.Usage.ProcessedPages)
}

func TestMistralOCRUsageAndPageValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		count      *int
		bad        bool
	}{
		{"missing", `{"pages":[]}`, nil, false},
		{"null", `{"pages":[],"usage_info":{"pages_processed":null}}`, nil, false},
		{"zero", `{"pages":[],"usage_info":{"pages_processed":0}}`, new(0), false},
		{"negative", `{"pages":[],"usage_info":{"pages_processed":-1}}`, nil, false},
		{"partial", `{"pages":[],"usage_info":{"pages_processed":1}}`, new(1), false},
		{"duplicate", `{"pages":[{"index":0,"markdown":""},{"index":0,"markdown":""}],"usage_info":{"pages_processed":1}}`, new(1), true},
		{"missing index", `{"pages":[{"markdown":""}],"usage_info":{"pages_processed":1}}`, new(1), true},
		{"outside", `{"pages":[{"index":1,"markdown":""}],"usage_info":{"pages_processed":1}}`, new(1), true},
		{"missing text", `{"pages":[{"index":0}],"usage_info":{"pages_processed":1}}`, new(1), true},
		{"blank text", `{"pages":[{"index":0,"markdown":""}],"usage_info":{"pages_processed":1}}`, new(1), false},
		{"negative index", `{"pages":[{"index":-1,"markdown":""}],"usage_info":{"pages_processed":1}}`, new(1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := decodeMistralOCR(strings.NewReader(tc.body), 1)
			if tc.bad {
				require.ErrorIs(t, err, ErrInvalidMediaRequest)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, response)
			require.Equal(t, tc.count, response.ProcessedPages)
		})
	}
	for _, body := range []string{`{"pages":[],"usage_info":{"pages_processed":1.5}}`, `{"pages":[],"usage_info":{"pages_processed":9999999999999999999999}}`, `{"pages":[]} {}`} {
		response, err := decodeMistralOCR(strings.NewReader(body), 1)
		require.Error(t, err)
		require.Nil(t, response)
	}
}

func TestMistralOCRRefusesUnsupportedRequests(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached provider") }))
	defer server.Close()
	c, err := newMistralOCRConnector("fixture-provider", mediaTestConfig(server.URL))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	reader := c.(DocumentRecognizer)
	valid := RecognitionRequest{Model: "ocr", Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeMistralOCR, URL: server.URL}, Document: UploadedFile{MediaType: "application/pdf", Bytes: []byte("pdf")}, Pages: 1}
	for _, change := range []func(*RecognitionRequest){
		func(r *RecognitionRequest) { r.Pages = 0 }, func(r *RecognitionRequest) { r.Document.Bytes = nil },
		func(r *RecognitionRequest) { r.Document.MediaType = "image/png" }, func(r *RecognitionRequest) { r.MaxTokens = new(1) },
	} {
		request := valid
		change(&request)
		response, err := reader.RecognizeDocument(t.Context(), &request)
		require.ErrorIs(t, err, ErrInvalidMediaRequest)
		require.Nil(t, response)
	}
	_, err = reader.RecognizeDocument(t.Context(), nil)
	require.ErrorIs(t, err, ErrInvalidMediaRequest)
	_, err = c.Chat(t.Context(), nil)
	require.ErrorIs(t, err, ErrTransportOperationUnsupported)
	_, err = c.ChatStream(t.Context(), nil)
	require.ErrorIs(t, err, ErrTransportOperationUnsupported)
	_, err = c.Embeddings(t.Context(), nil)
	require.ErrorIs(t, err, ErrTransportOperationUnsupported)
}
