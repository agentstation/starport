package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/sources"
	pkgsync "github.com/agentstation/starmap/pkg/sync"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestRefreshCandidateOwnsSourceAndTimeoutPolicy(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	syncer := &capturingAcquisitionSyncer{}
	runtime, err := newRuntime(client, syncer)
	require.NoError(t, err)

	timeout := 17 * time.Second
	state, err := runtime.RefreshCandidate(t.Context(), timeout)
	require.NoError(t, err)
	require.Equal(t, client.CurrentCatalogState().GenerationID, state.GenerationID)
	require.Equal(t, []sources.ID{sources.ProvidersID, sources.LocalCatalogID}, syncer.options.Sources)
	require.Equal(t, timeout, syncer.options.Timeout)
	require.True(t, syncer.hasDeadline)

	defaultSyncer := &capturingAcquisitionSyncer{}
	defaultRuntime, err := newRuntime(client, defaultSyncer)
	require.NoError(t, err)
	_, err = defaultRuntime.RefreshCandidate(t.Context(), 0)
	require.NoError(t, err)
	require.Equal(t, DefaultRefreshTimeout, defaultSyncer.options.Timeout)
}

type capturingAcquisitionSyncer struct {
	options     pkgsync.Options
	hasDeadline bool
}

func (s *capturingAcquisitionSyncer) Sync(
	ctx context.Context,
	options ...pkgsync.Option,
) (*pkgsync.Result, error) {
	for _, option := range options {
		option(&s.options)
	}
	_, s.hasDeadline = ctx.Deadline()
	return &pkgsync.Result{}, nil
}

func (*capturingAcquisitionSyncer) PublishObservations(
	context.Context,
	...sources.Observation,
) (starmap.Publication, error) {
	return starmap.Publication{}, nil
}

func TestAccountOfferingsEnterCatalogGeneration(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMockStore()
	runtime, err := OpenRuntime(ctx, store, "")
	require.NoError(t, err)

	observation := accountOfferingObservation(t, runtime.ControlPlane().Current().Catalog())
	before := runtime.ControlPlane().Current().GenerationID()
	publication, err := runtime.PublishObservations(ctx, observation)
	require.NoError(t, err)
	require.True(t, publication.Published)
	require.NotEqual(t, before, publication.GenerationID)
	require.Equal(t, publication.GenerationID, runtime.ControlPlane().Current().GenerationID())

	offering, err := runtime.ControlPlane().Current().Catalog().Offering(
		catalogs.ProviderIDOllama,
		"account-deployment",
	)
	require.NoError(t, err)
	require.NoError(t, runtime.ControlPlane().SetAdapter(
		testAdapterAvailability(catalogs.ProviderIDOllama, offering, true),
	))
	route, found := runtime.ControlPlane().Current().ResolveRoute("ollama/account-deployment")
	require.True(t, found)
	require.Equal(t, catalogs.ModelDefinitionID("account/local-chat"), route.DefinitionID)

	reopened, err := OpenRuntime(ctx, store, "")
	require.NoError(t, err)
	require.Equal(t, publication.GenerationID, reopened.ControlPlane().Current().GenerationID())
	_, err = reopened.ControlPlane().Current().Catalog().Offering(
		catalogs.ProviderIDOllama,
		"account-deployment",
	)
	require.NoError(t, err)
}

func accountOfferingObservation(t *testing.T, baseline *catalogs.Catalog) sources.Observation {
	t.Helper()
	builder, err := catalogs.NewBuilderFrom(baseline)
	require.NoError(t, err)
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "account", Name: "Account"}))
	require.NoError(t, builder.SetAuthorModel("account", catalogs.Model{
		ID: "local-chat", Name: "Local Chat",
		Authors: []catalogs.Author{{ID: "account", Name: "Account"}},
		Features: &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{
			Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
			Output: []catalogs.ModelModality{catalogs.ModelModalityText},
		}},
	}))
	require.NoError(t, builder.SetProviderModel(catalogs.ProviderIDOllama, catalogs.Model{
		ID: "account-deployment", ModelRef: "account/local-chat", Name: "Account deployment",
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
