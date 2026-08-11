package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestTenantOfferingsEnterCatalogGeneration(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	runtime, err := OpenRuntime(ctx, store, "")
	require.NoError(t, err)

	observation := tenantOfferingObservation(t, runtime.ControlPlane().Current().Catalog())
	before := runtime.ControlPlane().Current().GenerationID()
	publication, err := runtime.PublishObservations(ctx, observation)
	require.NoError(t, err)
	require.True(t, publication.Published)
	require.NotEqual(t, before, publication.GenerationID)
	require.Equal(t, publication.GenerationID, runtime.ControlPlane().Current().GenerationID())

	offering, err := runtime.ControlPlane().Current().Catalog().Offering(
		catalogs.ProviderIDOllama,
		"tenant-deployment",
	)
	require.NoError(t, err)
	require.NoError(t, runtime.ControlPlane().SetAdapter(
		testAdapterAvailability(catalogs.ProviderIDOllama, offering, true),
	))
	route, found := runtime.ControlPlane().Current().ResolveRoute("ollama/tenant-deployment")
	require.True(t, found)
	require.Equal(t, catalogs.ModelDefinitionID("tenant/local-chat"), route.DefinitionID)

	reopened, err := OpenRuntime(ctx, store, "")
	require.NoError(t, err)
	require.Equal(t, publication.GenerationID, reopened.ControlPlane().Current().GenerationID())
	_, err = reopened.ControlPlane().Current().Catalog().Offering(
		catalogs.ProviderIDOllama,
		"tenant-deployment",
	)
	require.NoError(t, err)
}

func tenantOfferingObservation(t *testing.T, baseline *catalogs.Catalog) sources.Observation {
	t.Helper()
	builder, err := catalogs.NewBuilderFrom(baseline)
	require.NoError(t, err)
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "tenant", Name: "Tenant"}))
	require.NoError(t, builder.SetAuthorModel("tenant", catalogs.Model{
		ID: "local-chat", Name: "Local Chat",
		Authors: []catalogs.Author{{ID: "tenant", Name: "Tenant"}},
		Features: &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{
			Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
			Output: []catalogs.ModelModality{catalogs.ModelModalityText},
		}},
	}))
	require.NoError(t, builder.SetProviderModel(catalogs.ProviderIDOllama, catalogs.Model{
		ID: "tenant-deployment", ModelRef: "tenant/local-chat", Name: "Tenant deployment",
		Status: catalogs.ModelStatusActive,
		Features: &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{
			Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
			Output: []catalogs.ModelModality{catalogs.ModelModalityText},
		}},
	}))
	catalog, err := catalogs.NewObservationCatalog(builder)
	require.NoError(t, err)
	observation, err := sources.NewObservation(
		sources.LocalCatalogID,
		catalog,
		sources.ObservationMetadata{
			ObservedAt:   time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
			Revision:     sources.Revision{Kind: sources.RevisionKindContentDigest},
			Completeness: sources.ObservationCompletenessComplete,
			Status:       sources.ObservationStatusSucceeded,
			Records:      sources.ObservationRecordCounts{Accepted: 2},
		},
	)
	require.NoError(t, err)
	return observation
}
