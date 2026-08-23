package router

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/registry"
)

func TestTenantOnlyBindsEndpointFromTenantMaterial(t *testing.T) {
	fixture := newEndpointBindingFixture(t)
	response, err := fixture.router.RouteWithFallback(t.Context(), fixture.request(keyring.BYOKOnly))
	require.NoError(t, err)
	require.Equal(t, "acme/opaque/model@001", response.ModelUsed)
	require.Equal(t, []string{
		"https://tenant.example/projects/tenant-project/models/opaque/model@001/chat/completions",
	}, fixture.endpoints())
	require.Equal(t, []string{"tenant"}, fixture.materialVersions())
	require.Zero(t, fixture.operator.calls.Load())
}

func TestOperatorAndTenantBindingsDoNotCross(t *testing.T) {
	fixture := newEndpointBindingFixture(t)
	_, err := fixture.router.RouteWithFallback(t.Context(), fixture.request(keyring.BYOKOnly))
	require.NoError(t, err)
	_, err = fixture.router.RouteWithFallback(t.Context(), fixture.request(keyring.OperatorFirst))
	require.NoError(t, err)
	require.Equal(t, []string{
		"https://tenant.example/projects/tenant-project/models/opaque/model@001/chat/completions",
		"https://operator.example/projects/operator-project/models/opaque/model@001/chat/completions",
	}, fixture.endpoints())
	require.Equal(t, []string{"tenant", "operator"}, fixture.materialVersions())
	require.Equal(t, int64(1), fixture.operator.calls.Load())
}

type endpointBindingFixture struct {
	router   ModelRouter
	operator *bindingMaterialSource
	mu       sync.Mutex
	seen     []bindingAttempt
}

type bindingAttempt struct {
	endpoint        string
	materialVersion string
}

func newEndpointBindingFixture(t *testing.T) *endpointBindingFixture {
	t.Helper()
	catalog, profile := endpointBindingCatalog(t)
	plane, err := runtimecatalog.Open(endpointBindingCatalogSource{state: starmap.CatalogState{
		Catalog: catalog, GenerationID: "endpoint-binding", Sequence: 1,
		GeneratedAt: time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC),
	}})
	require.NoError(t, err)
	operator := &bindingMaterialSource{material: endpointBindingMaterial(
		profile, "operator", "https://operator.example", "operator-project",
	)}
	fixture := &endpointBindingFixture{operator: operator}
	connector := &mockConnector{name: "acme", chatFunc: func(
		_ context.Context,
		request *connectors.ChatRequest,
	) (*connectors.ChatResponse, error) {
		fixture.mu.Lock()
		fixture.seen = append(fixture.seen, bindingAttempt{
			endpoint: request.Endpoint.URL, materialVersion: request.Credential.Version(),
		})
		fixture.mu.Unlock()
		return &connectors.ChatResponse{ID: "response", Model: request.Model}, nil
	}}
	runtimeRegistry, err := registry.Open(plane, []registry.Registration{{
		Provider: "acme", Connector: connector,
		Operations:      []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions},
		EndpointTypes:   []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		OperatorBaseURL: "https://operator.example",
		OperatorSource:  operator,
		RequiresAuth:    true,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtimeRegistry.Close()) })
	user := &embeddingUserResolver{material: endpointBindingMaterial(
		profile, "tenant", "https://tenant.example", "tenant-project",
	)}
	adapter := &endpointBindingRegistry{registry: runtimeRegistry}
	fixture.router = New(adapter, WithCatalog(plane), WithStoredCredentials(user))
	return fixture
}

func (f *endpointBindingFixture) request(strategy keyring.Strategy) *Request {
	return &Request{
		ChatRequest: &connectors.ChatRequest{
			Model:    "author/model",
			Messages: []connectors.Message{{Role: connectors.RoleUser, Content: "hello"}},
		},
		TenantID: "tenant-a",
		APIKeyConfig: &APIKeyConfig{
			CredentialStrategy: strategy,
			AllowedModels:      []string{"author/model"},
			AllowedProviders:   []string{"acme"},
		},
	}
}

func (f *endpointBindingFixture) endpoints() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]string, len(f.seen))
	for index, attempt := range f.seen {
		result[index] = attempt.endpoint
	}
	return result
}

func (f *endpointBindingFixture) materialVersions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]string, len(f.seen))
	for index, attempt := range f.seen {
		result[index] = attempt.materialVersion
	}
	return result
}

type endpointBindingCatalogSource struct{ state starmap.CatalogState }

func (s endpointBindingCatalogSource) CurrentCatalogState() starmap.CatalogState { return s.state }

func endpointBindingCatalog(
	t *testing.T,
) (*catalogs.Catalog, catalogs.ProviderCredentialProfile) {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "author", Name: "Author"}))
	features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{
		Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
		Output: []catalogs.ModelModality{catalogs.ModelModalityText},
	}}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{
		ID: "model", Name: "Model", Authors: []catalogs.Author{{ID: "author", Name: "Author"}},
		Features: features, Metadata: &catalogs.ModelMetadata{},
	}))
	profile := catalogs.ProviderCredentialProfile{
		ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
		Fields: []catalogs.ProviderCredentialFieldID{"api-key", "base-url", "project"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
		}},
		EndpointBindings: []catalogs.ProviderCredentialEndpointBinding{
			{Field: "base-url", Variable: "base_url", Format: catalogs.ProviderCredentialEndpointBindingURL},
			{Field: "project", Variable: "project", Format: catalogs.ProviderCredentialEndpointBindingPathSegment},
		},
	}
	require.NoError(t, builder.SetProvider(catalogs.Provider{
		ID: "acme", Name: "Acme",
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{
				{ID: "api-key", Kind: catalogs.ProviderCredentialFieldSecret, Required: true},
				{ID: "base-url", Kind: catalogs.ProviderCredentialFieldParameter, Required: true},
				{ID: "project", Kind: catalogs.ProviderCredentialFieldParameter, Required: true},
			},
			Profiles:  []catalogs.ProviderCredentialProfile{profile},
			Inference: catalogs.ProviderCredentialPlane{Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"}},
		},
		Inference: &catalogs.ProviderInference{
			BaseURL: "{base_url}",
			Endpoints: []catalogs.ProviderInferenceEndpoint{{
				Operation: catalogs.ProviderOperationChatCompletions,
				Type:      catalogs.EndpointTypeOpenAI,
				Path:      "/projects/{project}/models/{provider_model_id}/chat/completions",
			}},
		},
		Models: map[string]*catalogs.Model{"opaque/model@001": {
			ID: "opaque/model@001", ModelRef: "author/model", Name: "Acme Model",
			Status: catalogs.ModelStatusActive, Features: features, Metadata: &catalogs.ModelMetadata{},
		}},
	}))
	catalog, err := builder.Build()
	require.NoError(t, err)
	return catalog, profile
}

func endpointBindingMaterial(
	profile catalogs.ProviderCredentialProfile,
	version string,
	baseURL string,
	project string,
) credentials.Material {
	return credentials.NewMaterial(profile, map[catalogs.ProviderCredentialFieldID]string{
		"api-key": version + "-key", "base-url": baseURL, "project": project,
	}, credentials.MaterialMetadata{Version: version})
}

type bindingMaterialSource struct {
	material credentials.Material
	calls    atomic.Int64
}

func (s *bindingMaterialSource) ResolveMaterial(context.Context) (credentials.Material, error) {
	s.calls.Add(1)
	return s.material, nil
}

type endpointBindingRegistry struct{ registry *registry.Registry }

func (r *endpointBindingRegistry) Get(provider string) connectors.Connector {
	connector, _ := r.registry.Get(provider)
	return connector
}

func (r *endpointBindingRegistry) List() []string { return r.registry.ListProviders() }

func (r *endpointBindingRegistry) ResolveMaterial(
	ctx context.Context,
	provider string,
) (credentials.Material, error) {
	return r.registry.ResolveMaterial(ctx, provider)
}

func (r *endpointBindingRegistry) AcquireRuntime() (connectors.RuntimeLease, error) {
	return r.registry.AcquireRuntime()
}
