package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/stretchr/testify/require"
)

func TestHTTPCanonicalRemovalAfterSuccessfulInference(t *testing.T) {
	testHTTPCanonicalRemoval(t, "")
}

func TestSDKCanonicalRemovalAfterSuccessfulInference(t *testing.T) {
	python := os.Getenv("STARPORT_CATALOG_SDK_PYTHON")
	if python == "" {
		t.Skip("SDK qualification requires STARPORT_CATALOG_SDK_PYTHON")
	}
	testHTTPCanonicalRemoval(t, python)
}

func testHTTPCanonicalRemoval(t *testing.T, python string) {
	t.Helper()
	builder := catalogs.NewEmpty()
	author := catalogs.Author{ID: "author", Name: "Author"}
	require.NoError(t, builder.SetAuthor(author))
	features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "current", Name: "Current", Authors: []catalogs.Author{author}, Features: features}))
	require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "acme", Name: "Acme", Inference: &catalogs.ProviderInference{BaseURL: "https://provider.test/v1", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeOpenAI, Path: "/chat/completions"}}}, Models: map[string]*catalogs.Model{"opaque/model@002": {ID: "opaque/model@002", ModelRef: "author/current", Limits: &catalogs.ModelLimits{ContextWindow: 4096}, Status: catalogs.ModelStatusActive, Features: features}}}))
	accepted, err := builder.Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(aliasHTTPSource{state: starmap.CatalogState{Catalog: accepted, GenerationID: "canonical-present", Sequence: 1}})
	require.NoError(t, err)
	registration := func() registry.Registration {
		return registry.Registration{Provider: "acme", Connector: connectors.NewMockConnector(connectors.ProviderConfig{}), Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}, Anonymous: credentials.NewMaterial(catalogs.ProviderCredentialProfile{ID: "none", Primitive: catalogs.ProviderAuthenticationNone}, nil, credentials.MaterialMetadata{Version: "test"})}
	}
	reg, err := registry.Open(plane, []registry.Registration{registration()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reg.Close()) })
	s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, func(config *testServerConfig) { config.runtimeRegistry = reg })
	secret := createServerAPIKey(t, s, "removal-reader", []string{"models:read", "chat:write"})
	server := httptest.NewServer(s.Router())
	t.Cleanup(server.Close)
	request := func(method, path, body string) (int, string) {
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		require.NoError(t, err)
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, string(data)
	}
	for _, prefix := range []string{"/v1", "/api/v1"} {
		for _, stream := range []string{"false", "true"} {
			status, body := request(http.MethodPost, prefix+"/chat/completions", `{"model":"author/current","messages":[{"role":"user","content":"hello"}],"stream":`+stream+`}`)
			require.Equal(t, http.StatusOK, status, body)
			require.Contains(t, body, "mock")
		}
		status, body := request(http.MethodGet, prefix+"/models/author%2Fcurrent", "")
		require.Equal(t, http.StatusOK, status, body)
	}
	runSDK := func(state string) {
		t.Helper()
		if python == "" {
			return
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, python, "../../scripts/smoke_catalog_transition.py", state)
		command.Env = append(os.Environ(), "STARPORT_CATALOG_URL="+server.URL, "STARPORT_CATALOG_KEY="+secret)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	runSDK("present")
	empty, err := catalogs.NewEmpty().Build()
	require.NoError(t, err)
	candidate, err := reg.Prepare([]registry.Registration{registration()})
	require.NoError(t, err)
	defer func() { require.NoError(t, candidate.Close()) }()
	snapshot, err := plane.ReplaceRuntime(starmap.CatalogState{Catalog: empty, GenerationID: "canonical-removed", Sequence: 2}, candidate.Availability())
	require.NoError(t, err)
	require.NoError(t, reg.Publish(candidate, snapshot))
	for _, prefix := range []string{"/v1", "/api/v1"} {
		for _, stream := range []string{"false", "true"} {
			status, body := request(http.MethodPost, prefix+"/chat/completions", `{"model":"author/current","messages":[{"role":"user","content":"hello"}],"stream":`+stream+`}`)
			require.Equal(t, http.StatusNotFound, status, body)
			require.Contains(t, body, "not_found_error")
			require.NotContains(t, body, "data:")
			require.NotContains(t, body, "opaque/model@002")
		}
		status, body := request(http.MethodGet, prefix+"/models/author%2Fcurrent", "")
		require.Equal(t, http.StatusNotFound, status, body)
		status, body = request(http.MethodGet, prefix+"/models", "")
		require.Equal(t, http.StatusOK, status, body)
		require.NotContains(t, body, "author/current")
	}
	runSDK("removed")
}
