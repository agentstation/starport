package router

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
)

func TestRouteEmbeddingsUsesRequestCredentialPolicy(t *testing.T) {
	plane := embeddingTestCatalogPlane(t)
	connector := &mockConnector{name: "acme"}
	var providerCalls atomic.Int64
	connector.embeddingsFunc = func(_ context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
		providerCalls.Add(1)
		require.Equal(t, "opaque/embed@002", req.Model)
		require.Equal(t, "https://provider.test/v1/embeddings", req.Endpoint.URL)
		if req.Credential.Version() == "user" {
			return nil, &connectors.APIError{StatusCode: 401, Message: "user credential rejected"}
		}
		require.Equal(t, "operator", req.Credential.Version())
		return &connectors.EmbeddingsResponse{
			Object: "list", Model: req.Model,
			Data: []connectors.Embedding{{Object: "embedding", Index: 0, Embedding: []float32{0.25, 0.75}}},
		}, nil
	}
	runtime := &embeddingTestRuntime{
		snapshot: plane.Current(), connector: connector,
		operator: embeddingTestMaterial("operator"),
	}
	registry := &embeddingTestRegistry{runtime: runtime}
	storedKeys := &embeddingUserResolver{material: embeddingTestMaterial("user")}
	modelRouter := New(registry, WithCatalog(plane), WithStoredCredentials(storedKeys))

	response, err := modelRouter.RouteEmbeddings(t.Context(), &EmbeddingRequest{
		EmbeddingsRequest: &connectors.EmbeddingsRequest{Model: "author/embed", Input: "hello"},
		TenantID:          "tenant-a",
		APIKeyConfig: &APIKeyConfig{
			AllowedModels: []string{"author/embed"}, AllowedProviders: []string{"acme"},
			CredentialStrategy: keyring.BYOKFirst,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "acme/opaque/embed@002", response.ModelUsed)
	require.Equal(t, "acme/opaque/embed@002", response.Response.Model)
	require.Equal(t, []float32{0.25, 0.75}, response.Response.Data[0].Vector)
	require.Equal(t, 2, response.Attempts)
	require.Equal(t, int64(2), providerCalls.Load())
	require.Equal(t, int64(1), storedKeys.calls.Load())
	require.Equal(t, int64(1), runtime.operatorCalls.Load())
}

func TestRouteEmbeddingsUserOnlyNeverProbesOperatorMaterial(t *testing.T) {
	plane := embeddingTestCatalogPlane(t)
	runtime := &embeddingTestRuntime{
		snapshot: plane.Current(), connector: &mockConnector{name: "acme"},
		operator: embeddingTestMaterial("operator"),
	}
	modelRouter := New(
		&embeddingTestRegistry{runtime: runtime},
		WithCatalog(plane),
		WithStoredCredentials(&embeddingUserResolver{err: keyring.ErrKeyNotFound}),
	)

	_, err := modelRouter.RouteEmbeddings(t.Context(), &EmbeddingRequest{
		EmbeddingsRequest: &connectors.EmbeddingsRequest{Model: "author/embed", Input: "hello"},
		TenantID:          "tenant-a",
		APIKeyConfig:      &APIKeyConfig{CredentialStrategy: keyring.BYOKOnly},
	})
	require.Error(t, err)
	require.Zero(t, runtime.operatorCalls.Load())
	var providerFailure *failure.Failure
	require.ErrorAs(t, err, &providerFailure)
	require.Equal(t, "Provider credentials are not configured.", providerFailure.SafeMessage())
}

type embeddingCatalogSource struct{ state starmap.CatalogState }

func (s embeddingCatalogSource) CurrentCatalogState() starmap.CatalogState { return s.state }

func embeddingTestCatalogPlane(t *testing.T) *runtimecatalog.ControlPlane {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "author", Name: "Author"}))
	features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{
		Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
		Output: []catalogs.ModelModality{catalogs.ModelModalityEmbedding},
	}}
	metadata := &catalogs.ModelMetadata{Tags: []catalogs.ModelTag{catalogs.ModelTagEmbedding}}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{
		ID: "embed", Name: "Embedding model", Authors: []catalogs.Author{{ID: "author", Name: "Author"}},
		Features: features, Metadata: metadata,
	}))
	require.NoError(t, builder.SetProvider(catalogs.Provider{
		ID: "acme", Name: "Acme",
		Inference: &catalogs.ProviderInference{
			BaseURL: "https://provider.test/v1",
			Endpoints: []catalogs.ProviderInferenceEndpoint{{
				Operation: catalogs.ProviderOperationEmbeddings,
				Type:      catalogs.EndpointTypeOpenAI,
				Path:      "/embeddings",
			}},
		},
		Models: map[string]*catalogs.Model{"opaque/embed@002": {
			ID: "opaque/embed@002", ModelRef: "author/embed", Name: "Acme embedding",
			Status: catalogs.ModelStatusActive, Features: features, Metadata: metadata,
		}},
	}))
	catalog, err := builder.Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(embeddingCatalogSource{state: starmap.CatalogState{
		Catalog: catalog, GenerationID: "embedding-test", Sequence: 1,
		GeneratedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
	}})
	require.NoError(t, err)
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
		ProviderID: "acme", Registered: true,
		Operations:    []catalogs.ProviderOperation{catalogs.ProviderOperationEmbeddings},
		EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
	}))
	require.Len(t, plane.Current().RoutesForProvider("acme"), 1)
	return plane
}

type embeddingTestRegistry struct{ runtime *embeddingTestRuntime }

func (r *embeddingTestRegistry) Get(provider string) connectors.Connector {
	return r.runtime.Get(provider)
}
func (*embeddingTestRegistry) List() []string { return []string{"acme"} }
func (r *embeddingTestRegistry) ResolveMaterial(ctx context.Context, provider string) (credentials.Material, error) {
	return r.runtime.ResolveMaterial(ctx, provider)
}
func (r *embeddingTestRegistry) AcquireRuntime() (connectors.RuntimeLease, error) {
	return r.runtime, nil
}

type embeddingTestRuntime struct {
	snapshot  *runtimecatalog.RoutableSnapshot
	connector connectors.Connector
	operator  credentials.Material
	// operatorErr is how a deployment with no environment credential for this
	// provider behaves. A zero value keeps the environment plane available.
	operatorErr   error
	operatorCalls atomic.Int64
}

func (r *embeddingTestRuntime) Snapshot() *runtimecatalog.RoutableSnapshot { return r.snapshot }
func (r *embeddingTestRuntime) Get(provider string) connectors.Connector {
	if provider == "acme" {
		return r.connector
	}
	return nil
}
func (*embeddingTestRuntime) RequiresAuthentication(string) bool { return false }
func (r *embeddingTestRuntime) ResolveMaterial(context.Context, string) (credentials.Material, error) {
	r.operatorCalls.Add(1)
	if r.operatorErr != nil {
		return credentials.Material{}, r.operatorErr
	}
	return r.operator, nil
}
func (*embeddingTestRuntime) Release() {}

type embeddingUserResolver struct {
	material credentials.Material
	err      error
	calls    atomic.Int64
}

func (r *embeddingUserResolver) ResolveStoredMaterial(
	context.Context,
	string,
	catalogs.Provider,
) (credentials.Material, error) {
	r.calls.Add(1)
	return r.material, r.err
}

func embeddingTestMaterial(version string) credentials.Material {
	return credentials.NewMaterial(
		catalogs.ProviderCredentialProfile{ID: "none", Primitive: catalogs.ProviderAuthenticationNone},
		nil,
		credentials.MaterialMetadata{Version: version},
	)
}
