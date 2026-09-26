package server

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestHTTPStreamRetainsCatalogDuringRefresh(t *testing.T) {
	for _, prefix := range []string{"/v1", "/api/v1"} {
		t.Run(prefix, func(t *testing.T) {
			builder := catalogs.NewEmpty()
			author := catalogs.Author{ID: "author", Name: "Author"}
			require.NoError(t, builder.SetAuthor(author))
			features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
			require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "current", Name: "Current", Authors: []catalogs.Author{author}, Features: features}))
			require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "acme", Name: "Acme", Inference: &catalogs.ProviderInference{BaseURL: "https://provider.test/v1", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeOpenAI, Path: "/chat/completions"}}}, Models: map[string]*catalogs.Model{"opaque/model@002": {ID: "opaque/model@002", ModelRef: "author/current", Limits: &catalogs.ModelLimits{ContextWindow: 4096}, Status: catalogs.ModelStatusActive, Features: features}}}))
			accepted, err := builder.Build()
			require.NoError(t, err)
			plane, err := runtimecatalog.Open(aliasHTTPSource{state: starmap.CatalogState{Catalog: accepted, GenerationID: "stream-before", Sequence: 1}})
			require.NoError(t, err)
			release := make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			connector := &refreshStreamConnector{MockConnector: connectors.NewMockConnector(connectors.ProviderConfig{}), release: release, closed: make(chan struct{})}
			reg, err := registry.Open(plane, []registry.Registration{{Provider: "acme", Connector: connector, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}, Anonymous: credentials.NewMaterial(catalogs.ProviderCredentialProfile{ID: "none", Primitive: catalogs.ProviderAuthenticationNone}, nil, credentials.MaterialMetadata{})}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reg.Close()) })
			s := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, func(c *testServerConfig) { c.runtimeRegistry = reg })
			key := createServerAPIKey(t, s, "stream-reader", []string{"chat:write"})
			host := httptest.NewServer(s.Router())
			t.Cleanup(host.Close)
			t.Cleanup(resume)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			send := func() *http.Response {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, host.URL+prefix+"/chat/completions", strings.NewReader(`{"model":"author/current","messages":[{"role":"user","content":"hello"}],"stream":true}`))
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+key)
				req.Header.Set("Content-Type", "application/json")
				resp, err := host.Client().Do(req)
				require.NoError(t, err)
				return resp
			}
			first := send()
			defer first.Body.Close()
			require.Equal(t, http.StatusOK, first.StatusCode)
			reader := bufio.NewReader(first.Body)
			for {
				line, err := reader.ReadString('\n')
				require.NoError(t, err)
				if strings.Contains(line, "before-refresh") {
					break
				}
			}
			empty, err := catalogs.NewEmpty().Build()
			require.NoError(t, err)
			candidate, err := reg.Prepare(nil)
			require.NoError(t, err)
			snapshot, err := plane.ReplaceRuntime(starmap.CatalogState{Catalog: empty, GenerationID: "stream-after", Sequence: 2}, candidate.Availability())
			require.NoError(t, err)
			require.NoError(t, reg.Publish(candidate, snapshot))
			select {
			case <-connector.closed:
				t.Fatal("refresh closed the connector of an active stream")
			default:
			}
			second := send()
			body, err := io.ReadAll(second.Body)
			require.NoError(t, err)
			require.NoError(t, second.Body.Close())
			require.Equal(t, http.StatusNotFound, second.StatusCode, string(body))
			require.Contains(t, string(body), "not_found_error")
			require.NotContains(t, string(body), "data:")
			resume()
			remainder, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Contains(t, string(remainder), "after-refresh")
			require.Contains(t, string(remainder), "data: [DONE]")
			select {
			case <-connector.closed:
			case <-ctx.Done():
				t.Fatal("drained connector did not close after the stream")
			}
		})
	}
}

type refreshStreamConnector struct {
	*connectors.MockConnector
	release <-chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (c *refreshStreamConnector) ChatStream(ctx context.Context, request *connectors.ChatRequest) (connectors.ChatStream, error) {
	return &refreshCatalogStream{ctx: ctx, model: request.Model, release: c.release}, nil
}
func (c *refreshStreamConnector) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.MockConnector.Close()
}

type refreshCatalogStream struct {
	ctx     context.Context
	model   string
	release <-chan struct{}
	index   int
}

func (s *refreshCatalogStream) Recv() (*connectors.ChatStreamChunk, error) {
	if s.index >= 2 {
		return nil, io.EOF
	}
	text := "before-refresh"
	if s.index == 1 {
		select {
		case <-s.release:
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
		text = "after-refresh"
	}
	s.index++
	return &connectors.ChatStreamChunk{ID: "refresh-stream", Model: s.model, Choices: []connectors.StreamChoice{{Index: 0, Delta: connectors.MessageDelta{Content: text}}}}, nil
}
func (s *refreshCatalogStream) Close() error { return nil }
