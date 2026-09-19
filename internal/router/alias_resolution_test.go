package router

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/registry"
	"github.com/stretchr/testify/require"
)

func TestInferenceAliasUsesTargetPermissionAndExactProviderID(t *testing.T) {
	plane := embeddingTestCatalogPlane(t)
	builder, err := catalogs.NewBuilderFrom(plane.Current().Catalog())
	require.NoError(t, err)
	alias := catalogs.CanonicalAlias{ID: "author/old-embed", TargetID: "author/embed", PublisherID: "publisher", State: catalogs.CanonicalAliasActive}
	require.NoError(t, builder.SetCanonicalAliasRecords([]catalogs.CanonicalAlias{alias}))
	accepted, err := builder.Build()
	require.NoError(t, err)
	require.NoError(t, plane.Activate(starmap.CatalogState{Catalog: accepted, GenerationID: "renamed", Sequence: 2}))
	connector := &mockConnector{name: "acme"}
	calls := 0
	connector.embeddingsFunc = func(_ context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
		calls++
		require.Equal(t, "opaque/embed@002", req.Model)
		return &connectors.EmbeddingsResponse{Object: "list", Model: req.Model, Data: []connectors.Embedding{{Object: "embedding", Index: 0, Embedding: []float32{0.25}}}}, nil
	}
	runtime := &embeddingTestRuntime{snapshot: plane.Current(), connector: connector, operator: embeddingTestMaterial("operator")}
	router := New(&embeddingTestRegistry{runtime: runtime}, WithCatalog(plane))
	request := &EmbeddingRequest{EmbeddingsRequest: &connectors.EmbeddingsRequest{Model: string(alias.ID), Input: "hello"}, AccountID: "account-a", APIKeyConfig: &APIKeyConfig{AllowedModels: []string{"author/embed"}, CredentialStrategy: keyring.OperatorFirst}}
	response, err := router.RouteEmbeddings(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "acme/opaque/embed@002", response.ModelUsed)
	require.Equal(t, 1, calls)
	require.Equal(t, string(alias.ID), request.Model)
	request.APIKeyConfig.AllowedModels = []string{string(alias.ID)}
	_, err = router.RouteEmbeddings(t.Context(), request)
	require.Error(t, err, "alias spelling must not grant target permission")
	require.Equal(t, 1, calls)
	alias.State = catalogs.CanonicalAliasRemoved
	require.NoError(t, builder.SetCanonicalAliasRecords([]catalogs.CanonicalAlias{alias}))
	removed, err := builder.Build()
	require.NoError(t, err)
	require.NoError(t, plane.Activate(starmap.CatalogState{Catalog: removed, GenerationID: "removed", Sequence: 3}))
	retained := runtime.snapshot
	runtime.snapshot = plane.Current()
	request.APIKeyConfig.AllowedModels = []string{"author/embed"}
	_, err = router.RouteEmbeddings(t.Context(), request)
	require.ErrorIs(t, err, runtimecatalog.ErrModelNotCatalogued)
	require.Equal(t, 1, calls)
	require.True(t, retained.Names(string(alias.ID)))
}

func TestOrdinaryModelAliasLookupAddsNoAllocation(t *testing.T) {
	plane := embeddingTestCatalogPlane(t)
	names := []string{"author/embed"}
	require.Zero(t, testing.AllocsPerRun(100, func() {
		resolved, err := resolveModelAliases(plane.Current(), names)
		if err != nil || len(resolved) != 1 || resolved[0] != names[0] {
			t.Fatal("ordinary name changed")
		}
	}))
}

func TestAliasRemovalKeepsAdmittedRequestAndRejectsNewAttempts(t *testing.T) {
	plane := embeddingTestCatalogPlane(t)
	builder, err := catalogs.NewBuilderFrom(plane.Current().Catalog())
	require.NoError(t, err)
	alias := catalogs.CanonicalAlias{ID: "author/old-embed", TargetID: "author/embed", PublisherID: "publisher", State: catalogs.CanonicalAliasActive}
	require.NoError(t, builder.SetCanonicalAliasRecords([]catalogs.CanonicalAlias{alias}))
	accepted, err := builder.Build()
	require.NoError(t, err)
	require.NoError(t, plane.Activate(starmap.CatalogState{Catalog: accepted, GenerationID: "alias-active", Sequence: 2}))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entered := make(chan string, 1)
	release := make(chan struct{})
	var oldCalls, newCalls atomic.Int64
	old := &mockConnector{name: "acme", embeddingsFunc: func(ctx context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
		oldCalls.Add(1)
		entered <- req.Model
		select {
		case <-release:
			return &connectors.EmbeddingsResponse{Object: "list", Model: req.Model, Data: []connectors.Embedding{{Object: "embedding", Index: 0, Embedding: []float32{0.25}}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	registration := func(connector connectors.Connector) registry.Registration {
		return registry.Registration{Provider: "acme", Connector: connector,
			Operations:     []catalogs.ProviderOperation{catalogs.ProviderOperationEmbeddings},
			EndpointTypes:  []catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
			OperatorSource: &bindingMaterialSource{material: embeddingTestMaterial("operator")}}
	}
	runtime, err := registry.Open(plane, []registry.Registration{registration(old)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	modelRouter := New(&endpointBindingRegistry{registry: runtime}, WithCatalog(plane))
	request := func(model string) *EmbeddingRequest {
		return &EmbeddingRequest{EmbeddingsRequest: &connectors.EmbeddingsRequest{Model: model, Input: "hello"}, AccountID: "account-a",
			APIKeyConfig: &APIKeyConfig{AllowedModels: []string{"author/embed"}, CredentialStrategy: keyring.OperatorFirst}}
	}
	type outcome struct {
		response *EmbeddingResponse
		err      error
	}
	completed := make(chan outcome, 1)
	go func() {
		response, err := modelRouter.RouteEmbeddings(ctx, request(string(alias.ID)))
		completed <- outcome{response, err}
	}()
	select {
	case model := <-entered:
		require.Equal(t, "opaque/embed@002", model)
	case <-ctx.Done():
		t.Fatal("initial request did not reach the provider", ctx.Err())
	}
	alias.State = catalogs.CanonicalAliasRemoved
	require.NoError(t, builder.SetCanonicalAliasRecords([]catalogs.CanonicalAlias{alias}))
	removed, err := builder.Build()
	require.NoError(t, err)
	replacement := &mockConnector{name: "acme", embeddingsFunc: func(_ context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
		newCalls.Add(1)
		return &connectors.EmbeddingsResponse{Object: "list", Model: req.Model, Data: []connectors.Embedding{{Object: "embedding", Index: 0, Embedding: []float32{0.75}}}}, nil
	}}
	candidate, err := runtime.Prepare([]registry.Registration{registration(replacement)})
	require.NoError(t, err)
	defer func() { require.NoError(t, candidate.Close()) }()
	snapshot, err := plane.ReplaceRuntime(starmap.CatalogState{Catalog: removed, GenerationID: "alias-removed", Sequence: 3}, candidate.Availability())
	require.NoError(t, err)
	require.NoError(t, runtime.Publish(candidate, snapshot))
	response, err := modelRouter.RouteEmbeddings(ctx, request(string(alias.ID)))
	require.ErrorIs(t, err, runtimecatalog.ErrModelNotCatalogued)
	require.Nil(t, response)
	require.Zero(t, newCalls.Load())
	response, err = modelRouter.RouteEmbeddings(ctx, request("author/embed"))
	require.NoError(t, err)
	require.Equal(t, "alias-removed", response.CatalogSnapshot.GenerationID())
	require.Equal(t, []float32{0.75}, response.Response.Data[0].Vector)
	close(release)
	select {
	case result := <-completed:
		require.NoError(t, result.err)
		require.NotNil(t, result.response)
		require.Equal(t, "alias-active", result.response.CatalogSnapshot.GenerationID())
		require.Equal(t, []float32{0.25}, result.response.Response.Data[0].Vector)
		require.Equal(t, "acme/opaque/embed@002", result.response.ModelUsed)
	case <-ctx.Done():
		t.Fatal("admitted request did not finish", ctx.Err())
	}
	require.Equal(t, int64(1), oldCalls.Load())
	require.Equal(t, int64(1), newCalls.Load())
}
