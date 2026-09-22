package connectors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/agentstation/starmap/pkg/catalogs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecognitionUsageRetainsPresenceAndDimensions(t *testing.T) {
	missing, err := RecognitionResponseToInference(&RecognitionResponse{})
	require.NoError(t, err)
	require.Nil(t, missing.Usage)
	zero, err := RecognitionResponseToInference(&RecognitionResponse{Usage: &MediaUsage{}})
	require.NoError(t, err)
	require.NotNil(t, zero.Usage)
	measured, err := RecognitionResponseToInference(&RecognitionResponse{Usage: &MediaUsage{
		InputTokens: 100, OutputTokens: 30, TotalTokens: 130, ReasoningTokens: 10, CacheReadTokens: 20, CacheWriteTokens: 5, AudioInputTokens: 10, AudioOutputTokens: 4,
	}})
	require.NoError(t, err)
	require.Equal(t, 100, measured.Usage.InputTokens)
	require.Equal(t, 30, measured.Usage.OutputTokens)
	require.Equal(t, 10, measured.Usage.ReasoningTokens)
	require.Equal(t, 20, measured.Usage.CacheReadTokens)
	require.Equal(t, 5, measured.Usage.CacheWriteTokens)
	require.Equal(t, 10, measured.Usage.AudioInputTokens)
	require.Equal(t, 4, measured.Usage.AudioOutputTokens)
	cloned := measured.Clone()
	cloned.Usage.InputTokens = 1
	require.Equal(t, 100, measured.Usage.InputTokens)
}

func TestGeminiBilledOutputIncludesThinkingOnce(t *testing.T) {
	measured := convertGeminiUsage(geminiUsageMetadata{
		PromptTokenCount: 100, CandidatesTokenCount: 20, ThoughtsTokenCount: 10, TotalTokenCount: 130, CachedContentTokenCount: 15,
	})
	require.Equal(t, 30, measured.CompletionTokens)
	require.Equal(t, 10, measured.CompletionTokensDetails.ReasoningTokens)
	require.Equal(t, 130, measured.TotalTokens)
	require.Equal(t, 15, measured.PromptTokensDetails.CachedTokens)
}

func TestGeminiUsagePresencePreservesExplicitZero(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		reported      bool
	}{
		{"absent", `{}`, false},
		{"null", `{"usageMetadata":null}`, false},
		{"empty", `{"usageMetadata":{}}`, false},
		{"null counts", `{"usageMetadata":{"promptTokenCount":null,"totalTokenCount":null}}`, false},
		{"zero", `{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw geminiResponse
			require.NoError(t, json.Unmarshal([]byte(tc.payload), &raw))
			connector := &googleBaseConnector{}
			converted := connector.convertToOpenAIResponse(&raw, &ChatRequest{Model: "document"})
			require.Equal(t, tc.reported, converted.usageReported)
			require.Zero(t, converted.Usage.TotalTokens)
		})
	}
}

func TestGoogleRecognitionRetainsUsageWithoutCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/recognize", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":0,"thoughtsTokenCount":10,"totalTokenCount":110}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	connector, err := NewGoogleAIStudioConnector(ProviderConfig{BaseURL: server.URL})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	response, err := connector.RecognizeDocument(t.Context(), &RecognitionRequest{
		MediaTarget: MediaTarget{Model: "document", Credential: testGoogleMaterial("test-key"), Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeGoogle, URL: server.URL + "/recognize"}},
		Document:    UploadedFile{Bytes: []byte("document"), MediaType: "application/pdf"}, Pages: 1,
	})
	require.NoError(t, err)
	require.Empty(t, response.Pages)
	canonical, err := RecognitionResponseToInference(response)
	require.NoError(t, err)
	require.NotNil(t, canonical.Usage)
	require.Equal(t, 100, canonical.Usage.InputTokens)
	require.Equal(t, 10, canonical.Usage.OutputTokens)
	require.Equal(t, 10, canonical.Usage.ReasoningTokens)
	require.Equal(t, 110, canonical.Usage.TotalTokens)
}
