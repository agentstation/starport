package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/server"
	"github.com/stretchr/testify/require"
)

// TestLoaderComposedGatewayStreamsOnInstallationDefaults composes the gateway
// the way `starport dev` does: through the configuration loader with no
// explicit destination policy. The literal configurations in the other tests
// leave InferenceDestinationApprovals nil by construction, so they never saw
// the loader materialize an empty set that denies every destination. The mock
// connector keeps the request off the network. The policy bind that returned
// 503 runs in the router before any connector call.
func TestLoaderComposedGatewayStreamsOnInstallationDefaults(t *testing.T) {
	cfg, err := config.NewLoader().WithEnvFiles().WithEnvironment(map[string]string{
		"OPENAI_API_KEY":                       "sk-test-key",
		"STARPORT_HOME":                        filepath.Join(t.TempDir(), "installation"),
		"STARPORT_CATALOG_NETWORK_MODE":        "offline",
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).LoadDevelopment(t.Context())
	require.NoError(t, err)
	development, err := NewDevelopment(t.Context(), cfg, func(options *buildOptions) {
		options.factories.newConnector = func(
			_ string,
			_ []catalogs.EndpointType,
			providerConfig connectors.ProviderConfig,
		) (connectors.Connector, error) {
			return connectors.NewMockConnector(providerConfig), nil
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, development.Close(context.Background())) })
	httpServer, ok := development.application.httpServer.(*server.Server)
	require.True(t, ok)

	body := `{"model":"openai/gpt-4o-mini","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"Hello"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+development.APIKey())
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	httpServer.Router().ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/event-stream"), recorder.Header().Get("Content-Type"))
	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	require.Equal(t, "data: [DONE]", lines[len(lines)-1], recorder.Body.String())
	require.Greater(t, len(lines), 1, recorder.Body.String())
}
