package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestProtocolMiddlewareSelectsErrorDialect(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1024})
	tests := []struct {
		name      string
		path      string
		codeType  string
		errorType string
	}{
		{name: "OpenAI", path: "/v1/models", codeType: "string", errorType: "authentication_error"},
		{name: "OpenRouter", path: "/api/v1/models", codeType: "number", errorType: "authentication_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			var response map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			errorValue := response["error"].(map[string]any)
			switch test.codeType {
			case "number":
				require.Equal(t, float64(http.StatusUnauthorized), errorValue["code"])
				metadata := errorValue["metadata"].(map[string]any)
				require.Equal(t, test.errorType, metadata["error_type"])
			case "string":
				require.Equal(t, test.errorType, errorValue["type"])
			}
		})
	}
}

func TestProtocolRoutesUseSelectedCodec(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	apiKey := "test-protocol-route-key"
	hash := sha256.Sum256([]byte(apiKey))
	_, err := server.identities.Create(context.Background(), identity.APIKey{
		ID:        "protocol-route-key",
		Name:      "protocol_route_key",
		Hash:      hex.EncodeToString(hash[:]),
		Scopes:    []string{"*"},
		Active:    true,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	body := `{"model":"mock/test-model","models":["mock/test-model"],"messages":[{"role":"user","content":"hello"}]}`

	openAIRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	openAIRequest.Header.Set("Authorization", "Bearer "+apiKey)
	openAIRecorder := httptest.NewRecorder()
	server.router.ServeHTTP(openAIRecorder, openAIRequest)
	require.Equal(t, http.StatusBadRequest, openAIRecorder.Code)
	var openAIResponse map[string]any
	require.NoError(t, json.Unmarshal(openAIRecorder.Body.Bytes(), &openAIResponse))
	require.Equal(t, "invalid_request_error", openAIResponse["error"].(map[string]any)["type"])

	openRouterRequest := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completions", strings.NewReader(body))
	openRouterRequest.Header.Set("Authorization", "Bearer "+apiKey)
	openRouterRecorder := httptest.NewRecorder()
	server.router.ServeHTTP(openRouterRecorder, openRouterRequest)
	require.Equal(t, http.StatusOK, openRouterRecorder.Code, openRouterRecorder.Body.String())
}
