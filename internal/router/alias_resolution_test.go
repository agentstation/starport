package router

import (
	"context"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
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
