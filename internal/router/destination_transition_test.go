package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/registry"
	"github.com/stretchr/testify/require"
)

func TestLiveCatalogDestinationTransition(t *testing.T) {
	for _, role := range []keyring.CredentialSource{keyring.SourceEnvironment, keyring.SourceShared, keyring.SourceBYOK} {
		for _, streaming := range []bool{false, true} {
			for _, mutation := range []string{"host", "port", "path", "placement"} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", role, streaming, mutation), func(t *testing.T) {
					var calls atomic.Int64
					serve := func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						require.True(t, r.Header.Get("Authorization") == "Bearer transition-secret" || r.Header.Get("X-Changed-Key") == "Bearer transition-secret")
						var body struct {
							Stream bool `json:"stream"`
						}
						require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
						if body.Stream {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprint(w, "data: {\"id\":\"transition\",\"model\":\"opaque/model@001\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
						} else {
							fmt.Fprint(w, `{"id":"transition","model":"opaque/model@001","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`)
						}
					}
					initialServer := httptest.NewServer(http.HandlerFunc(serve))
					defer initialServer.Close()
					nextServer := httptest.NewServer(http.HandlerFunc(serve))
					defer nextServer.Close()
					base, profile := endpointBindingCatalog(t)
					profile.Fields = []catalogs.ProviderCredentialFieldID{"api-key"}
					profile.EndpointBindings = nil
					build := func(origin, path, header string) *catalogs.Catalog {
						builder, err := catalogs.NewBuilderFrom(base)
						require.NoError(t, err)
						provider, err := base.Provider("acme")
						require.NoError(t, err)
						selected := profile
						selected.Placements = append([]catalogs.ProviderCredentialPlacement(nil), profile.Placements...)
						selected.Placements[0].Name = header
						provider.Credentials.Profiles = []catalogs.ProviderCredentialProfile{selected}
						provider.Inference.BaseURL = origin
						provider.Inference.Endpoints[0].Path = path
						require.NoError(t, builder.SetProvider(provider))
						result, err := builder.Build()
						require.NoError(t, err)
						return result
					}
					initial := build(initialServer.URL, "/v1/chat/completions", "Authorization")
					state := func(c *catalogs.Catalog, sequence uint64) starmap.CatalogState {
						return starmap.CatalogState{Catalog: c, GenerationID: fmt.Sprintf("transition-%d", sequence), Sequence: sequence, GeneratedAt: time.Now()}
					}
					plane, err := runtimecatalog.Open(endpointBindingCatalogSource{state: state(initial, 1)})
					require.NoError(t, err)
					material := func(c *catalogs.Catalog) credentials.Material {
						p, err := c.Provider("acme")
						require.NoError(t, err)
						return credentials.NewMaterial(p.Credentials.Profiles[0], map[catalogs.ProviderCredentialFieldID]string{"api-key": "transition-secret"}, credentials.MaterialMetadata{Handle: "transition"})
					}
					source := &bindingMaterialSource{material: material(initial)}
					user := &embeddingUserResolver{material: material(initial)}
					registrations := func() []registry.Registration {
						connector, err := connectors.NewOpenAIConnector(connectors.ProviderConfig{BaseURL: initialServer.URL, Timeout: time.Second, Enabled: true})
						require.NoError(t, err)
						return []registry.Registration{{Provider: "acme", Connector: connector, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}, OperatorSource: source, RequiresAuth: true}}
					}
					reg, err := registry.Open(plane, registrations())
					require.NoError(t, err)
					defer reg.Close()
					approve := func(c *catalogs.Catalog) *credentials.DestinationApprovals {
						p, err := c.Provider("acme")
						require.NoError(t, err)
						policy, err := providers.CompileDestinationPolicy(p, string(role), profile.ID, "", nil)
						require.NoError(t, err)
						result, err := credentials.NewDestinationApprovals(nil, policy)
						require.NoError(t, err)
						return result
					}
					router := New(&endpointBindingRegistry{registry: reg}, WithCatalog(plane), WithStoredCredentials(user), WithDestinationApprovals(approve(initial)))
					request := func() error {
						strategy := keyring.OperatorFirst
						if role == keyring.SourceBYOK {
							strategy = keyring.BYOKOnly
						}
						req := (&endpointBindingFixture{}).request(strategy)
						req.ChatRequest.Stream = streaming
						if streaming {
							stream, err := router.RouteStream(t.Context(), req)
							if err != nil {
								return err
							}
							defer stream.Close()
							_, err = stream.Read()
							return err
						}
						_, err := router.RouteWithFallback(t.Context(), req)
						return err
					}
					require.NoError(t, request())
					require.Equal(t, int64(1), calls.Load())
					origin, path, header := initialServer.URL, "/v1/chat/completions", "Authorization"
					switch mutation {
					case "host":
						origin = strings.Replace(origin, "127.0.0.1", "localhost", 1)
					case "port":
						origin = nextServer.URL
					case "path":
						path = "/changed/chat/completions"
					case "placement":
						header = "X-Changed-Key"
					}
					next := build(origin, path, header)
					source.material = material(next)
					user.material = material(next)
					candidate, err := reg.Prepare(registrations())
					require.NoError(t, err)
					snapshot, err := plane.ReplaceRuntime(state(next, 2), candidate.Availability())
					require.NoError(t, err)
					require.NoError(t, reg.Publish(candidate, snapshot))
					require.Error(t, request())
					require.Equal(t, int64(1), calls.Load(), "catalog changes must not expand destination approval")
					WithDestinationApprovals(approve(next))(router.(*modelRouter))
					require.NoError(t, request())
					require.Equal(t, int64(2), calls.Load(), "explicit approval must enable the valid replacement")
				})
			}
		}
	}
}
