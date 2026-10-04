package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/server"
	"github.com/stretchr/testify/require"
)

// relayUpstream is an OpenAI-compatible fixture at an operator-approved
// origin. It records each request so the test can prove the path and the
// credential that reached it.
type relayUpstream struct {
	mu       sync.Mutex
	requests []relayRequest
}

type relayRequest struct {
	path          string
	authorization string
}

func (u *relayUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.requests = append(u.requests, relayRequest{path: r.URL.Path, authorization: r.Header.Get("Authorization")})
	u.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != "/relay/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range []string{
		`{"id":"chatcmpl-relay","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"relay-origin-reply"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-relay","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"chatcmpl-relay","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
		`[DONE]`,
	} {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
}

func (u *relayUpstream) seen() []relayRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]relayRequest(nil), u.requests...)
}

// fixtureOriginConnector wraps the production connector. It forwards only a
// bound endpoint at the fixture origin and records any other endpoint, so no
// request leaves the test host.
type fixtureOriginConnector struct {
	connectors.Connector
	origin  string
	refused *refusedEndpoints
}

type refusedEndpoints struct {
	mu   sync.Mutex
	urls []string
}

func (r *refusedEndpoints) add(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls = append(r.urls, url)
}

func (r *refusedEndpoints) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.urls...)
}

var errFixtureOriginOnly = errors.New("test connector forwards only the fixture origin")

func (c fixtureOriginConnector) admit(endpoint connectors.InferenceEndpoint) error {
	if strings.HasPrefix(endpoint.URL, c.origin+"/") {
		return nil
	}
	c.refused.add(endpoint.URL)
	return errFixtureOriginOnly
}

func (c fixtureOriginConnector) Chat(ctx context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	if err := c.admit(req.Endpoint); err != nil {
		return nil, err
	}
	return c.Connector.Chat(ctx, req)
}

func (c fixtureOriginConnector) ChatStream(ctx context.Context, req *connectors.ChatRequest) (connectors.ChatStream, error) {
	if err := c.admit(req.Endpoint); err != nil {
		return nil, err
	}
	return c.Connector.ChatStream(ctx, req)
}

func (c fixtureOriginConnector) Embeddings(ctx context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
	if err := c.admit(req.Endpoint); err != nil {
		return nil, err
	}
	return c.Connector.Embeddings(ctx, req)
}

// TestLoaderComposedGatewayStreamsThroughApprovedInferenceOrigin composes the
// gateway through the loader with STARPORT_OPENAI_INFERENCE_BASE_URL. The
// production HTTP connector streams environment material to the approved
// origin. BYOK material keeps the catalog origin, so the router never sends an
// account credential to the operator origin.
func TestLoaderComposedGatewayStreamsThroughApprovedInferenceOrigin(t *testing.T) {
	upstream := &relayUpstream{}
	relay := httptest.NewServer(upstream)
	t.Cleanup(relay.Close)
	origin := relay.URL + "/relay"

	cfg, err := config.NewLoader().WithEnvFiles().WithEnvironment(map[string]string{
		"OPENAI_API_KEY":                       "sk-test-key",
		"STARPORT_OPENAI_INFERENCE_BASE_URL":   origin + "/",
		"STARPORT_HOME":                        filepath.Join(t.TempDir(), "installation"),
		"STARPORT_CATALOG_NETWORK_MODE":        "offline",
		"STARPORT_CATALOG_ACQUISITION_ENABLED": "false",
	}).LoadDevelopment(t.Context())
	require.NoError(t, err)
	refused := &refusedEndpoints{}
	development, err := NewDevelopment(t.Context(), cfg, func(options *buildOptions) {
		production := options.factories.newConnector
		options.factories.newConnector = func(
			provider string,
			endpointTypes []catalogs.EndpointType,
			providerConfig connectors.ProviderConfig,
		) (connectors.Connector, error) {
			connector, err := production(provider, endpointTypes, providerConfig)
			if err != nil {
				return nil, err
			}
			return fixtureOriginConnector{Connector: connector, origin: origin, refused: refused}, nil
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, development.Close(context.Background())) })
	httpServer, ok := development.application.httpServer.(*server.Server)
	require.True(t, ok)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+development.APIKey())
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		httpServer.Router().ServeHTTP(recorder, request)
		return recorder
	}
	chat := `{"model":"openai/gpt-4o-mini","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"Hello"}]}`

	recorder := call(http.MethodPost, "/api/v1/chat/completions", chat)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/event-stream"), recorder.Header().Get("Content-Type"))
	require.Contains(t, recorder.Body.String(), "relay-origin-reply")
	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	require.Equal(t, "data: [DONE]", lines[len(lines)-1], recorder.Body.String())
	require.Equal(t, []relayRequest{{path: "/relay/v1/chat/completions", authorization: "Bearer sk-test-key"}}, upstream.seen())
	require.Empty(t, refused.list())

	recorder = call(http.MethodPut, "/api/v1/admin/accounts/default", `{"credential_strategy":"byok_only"}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	recorder = call(http.MethodPut, "/api/v1/accounts/default/byok/openai", `{"credentials":{"api-key":"sk-byok-key"}}`)
	require.True(t, recorder.Code == http.StatusOK || recorder.Code == http.StatusCreated, recorder.Body.String())

	recorder = call(http.MethodPost, "/api/v1/chat/completions", chat)
	require.NotEqual(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "relay-origin-reply")
	require.Equal(t, []string{"https://api.openai.com/v1/chat/completions"}, refused.list())
	require.Len(t, upstream.seen(), 1, "BYOK material reached the operator origin")
}
