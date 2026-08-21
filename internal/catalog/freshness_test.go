package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/catalogs/evidence"
	"github.com/agentstation/starmap/pkg/provenance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

type staticSnapshotSource struct {
	snapshot *RoutableSnapshot
}

func (s staticSnapshotSource) Current() *RoutableSnapshot { return s.snapshot }

func newFreshnessTestStore(t *testing.T) (*GenerationStore, storage.KVStore) {
	t.Helper()
	store := storage.NewMockStore()
	t.Cleanup(func() { _ = store.Close() })
	generations, err := NewGenerationStore(store)
	require.NoError(t, err)
	return generations, store
}

// freshnessModelFact declares one authored model offered by the test provider
// with token pricing, the minimum shape the catalog validator accepts.
type freshnessModelFact struct {
	slug        string
	inputPer1M  float64
	outputPer1M float64
}

func freshnessTestCatalog(t testing.TB, facts []freshnessModelFact, provenanceMap provenance.Map) *catalogs.Catalog {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "author", Name: "Author"}))
	modalities := catalogs.ModelModalities{
		Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
		Output: []catalogs.ModelModality{catalogs.ModelModalityText},
	}
	providerModels := map[string]*catalogs.Model{}
	for _, fact := range facts {
		require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{
			ID:       fact.slug,
			Name:     fact.slug,
			Authors:  []catalogs.Author{{ID: "author", Name: "Author"}},
			Metadata: &catalogs.ModelMetadata{},
			Features: &catalogs.ModelFeatures{Modalities: modalities},
		}))
		providerModels[fact.slug+"@1"] = &catalogs.Model{
			ID:       fact.slug + "@1",
			ModelRef: catalogs.ModelDefinitionID("author/" + fact.slug),
			Name:     fact.slug,
			Status:   catalogs.ModelStatusActive,
			Metadata: &catalogs.ModelMetadata{},
			Features: &catalogs.ModelFeatures{Modalities: modalities},
			Pricing: &catalogs.ModelPricing{
				Currency: catalogs.ModelPricingCurrencyUSD,
				Tokens: &catalogs.ModelTokenPricing{
					Input:  &catalogs.ModelTokenCost{Per1M: fact.inputPer1M},
					Output: &catalogs.ModelTokenCost{Per1M: fact.outputPer1M},
				},
			},
		}
	}
	require.NoError(t, builder.SetProvider(catalogs.Provider{
		ID:   "provider",
		Name: "Provider",
		Inference: &catalogs.ProviderInference{
			BaseURL: "https://provider.test",
			Endpoints: []catalogs.ProviderInferenceEndpoint{{
				Operation: catalogs.ProviderOperationChatCompletions,
				Type:      catalogs.EndpointTypeOpenAI,
				Path:      "/v1/chat/completions",
			}},
		},
		Models: providerModels,
	}))
	if provenanceMap != nil {
		builder.SetProvenance(provenanceMap)
	}
	catalog, err := builder.Build()
	require.NoError(t, err)
	return catalog
}

func TestCatalogMetadataExposesManifest(t *testing.T) {
	ctx := context.Background()
	generations, _ := newFreshnessTestStore(t)
	generatedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	generation := runtimeTestGeneration(t, "gen-meta-1", testEmptyCatalog(t, "openai"), generatedAt)
	require.NoError(t, generations.Commit(ctx, generation, ""))

	state := runtimeTestState(t, generation)
	snapshot := newRoutableSnapshot(state, 7, nil)
	service := NewFreshnessService(staticSnapshotSource{snapshot}, generations)
	service.now = func() time.Time { return generatedAt.Add(90 * time.Second) }

	metadata, err := service.Metadata(ctx)
	require.NoError(t, err)
	assert.Equal(t, "gen-meta-1", metadata.GenerationID)
	assert.Equal(t, generatedAt, metadata.GeneratedAt)
	assert.Equal(t, int64(90), metadata.AgeSeconds)
	assert.Equal(t, uint64(1), metadata.CatalogSequence)
	assert.Equal(t, uint64(7), metadata.AvailabilityRevision)
	assert.Equal(t, state.PayloadChecksum, metadata.PayloadChecksum)
	assert.Equal(t, generation.Manifest.Payload.SizeBytes, metadata.PayloadSizeBytes)
	assert.Equal(t, generation.Manifest.SchemaVersion, metadata.SchemaVersion)
	assert.True(t, metadata.ManifestAvailable)
	assert.Empty(t, metadata.ManifestUnavailableReason)
	assert.Equal(t, string(catalogs.GenerationCompletenessComplete), metadata.Completeness)
	assert.False(t, metadata.Degraded)
	assert.Empty(t, metadata.DegradationReasons)
	assert.Equal(t, string(catalogs.GenerationValidationPassed), metadata.Validation.Status)
	assert.Equal(t, generation.Manifest.Validation.ValidatedAt, metadata.Validation.ValidatedAt)
	assert.Equal(t, "sync-gen-meta-1", metadata.SyncRunID)
	require.Len(t, metadata.SourceObservations, 1)
	assert.Equal(t, string(evidence.LocalCatalogID), metadata.SourceObservations[0].Source)
	assert.Equal(t, string(evidence.ObservationCompletenessComplete), metadata.SourceObservations[0].Completeness)
	assert.Equal(t, string(evidence.ObservationStatusSucceeded), metadata.SourceObservations[0].Status)

	// A snapshot without a stored generation record (bootstrap) still reports
	// its scalar identity, and says loudly why manifest detail is missing.
	emptyGenerations, _ := newFreshnessTestStore(t)
	bootstrapped := NewFreshnessService(staticSnapshotSource{snapshot}, emptyGenerations)
	bootstrapped.now = func() time.Time { return generatedAt.Add(90 * time.Second) }
	partial, err := bootstrapped.Metadata(ctx)
	require.NoError(t, err)
	assert.Equal(t, "gen-meta-1", partial.GenerationID)
	assert.False(t, partial.ManifestAvailable)
	assert.NotEmpty(t, partial.ManifestUnavailableReason)
}

func TestDiffModelsAndPrices(t *testing.T) {
	ctx := context.Background()
	generations, _ := newFreshnessTestStore(t)
	baseTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	before := freshnessTestCatalog(t, []freshnessModelFact{
		{slug: "alpha", inputPer1M: 1, outputPer1M: 2},
		{slug: "gamma", inputPer1M: 5, outputPer1M: 10},
	}, nil)
	after := freshnessTestCatalog(t, []freshnessModelFact{
		{slug: "alpha", inputPer1M: 3, outputPer1M: 2},
		{slug: "beta", inputPer1M: 7, outputPer1M: 14},
	}, nil)

	generationBefore := runtimeTestGeneration(t, "gen-diff-1", before, baseTime)
	generationAfter := runtimeTestGeneration(t, "gen-diff-2", after, baseTime.Add(time.Hour))
	require.NoError(t, generations.Commit(ctx, generationBefore, ""))
	require.NoError(t, generations.Commit(ctx, generationAfter, "gen-diff-1"))

	snapshot := newRoutableSnapshot(runtimeTestState(t, generationAfter), 1, nil)
	service := NewFreshnessService(staticSnapshotSource{snapshot}, generations)

	diff, err := service.Changes(ctx)
	require.NoError(t, err)
	assert.True(t, diff.Available)
	assert.Equal(t, "gen-diff-1", diff.FromGenerationID)
	assert.Equal(t, "gen-diff-2", diff.ToGenerationID)
	assert.Equal(t, baseTime, diff.FromGeneratedAt)
	assert.Equal(t, baseTime.Add(time.Hour), diff.ToGeneratedAt)
	assert.False(t, diff.SemanticallyEqual)
	assert.Equal(t, []string{"author/beta"}, diff.ModelsAdded)
	assert.Equal(t, []string{"author/gamma"}, diff.ModelsRemoved)
	require.Len(t, diff.OfferingsAdded, 1)
	assert.Equal(t, OfferingChange{
		Provider: "provider", ProviderModelID: "beta@1", DefinitionID: "author/beta",
	}, diff.OfferingsAdded[0])
	require.Len(t, diff.OfferingsRemoved, 1)
	assert.Equal(t, OfferingChange{
		Provider: "provider", ProviderModelID: "gamma@1", DefinitionID: "author/gamma",
	}, diff.OfferingsRemoved[0])
	require.Len(t, diff.PriceChanges, 1)
	assert.Equal(t, PriceChange{
		Provider: "provider", ProviderModelID: "alpha@1", DefinitionID: "author/alpha",
		Field: "input", PreviousPer1M: 1, CurrentPer1M: 3,
	}, diff.PriceChanges[0])

	// A store with only one accepted generation has nothing to diff against.
	// That is reported plainly, never as an error or an empty diff.
	firstOnly, _ := newFreshnessTestStore(t)
	require.NoError(t, firstOnly.Commit(ctx, runtimeTestGeneration(t, "gen-solo", before, baseTime), ""))
	solo := NewFreshnessService(staticSnapshotSource{snapshot}, firstOnly)
	soloDiff, err := solo.Changes(ctx)
	require.NoError(t, err)
	assert.False(t, soloDiff.Available)
	assert.NotEmpty(t, soloDiff.Reason)
}

func TestDiffSkipsProvenanceOnlyChange(t *testing.T) {
	ctx := context.Background()
	generations, _ := newFreshnessTestStore(t)
	baseTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	facts := []freshnessModelFact{{slug: "alpha", inputPer1M: 1, outputPer1M: 2}}

	plain := freshnessTestCatalog(t, facts, nil)
	annotated := freshnessTestCatalog(t, facts, provenance.Map{
		"provider:provider:name": {{
			Source:    "test-source",
			Field:     "name",
			Value:     "Provider",
			Timestamp: baseTime,
			Reason:    "provenance-only churn",
		}},
	})

	generationBefore := runtimeTestGeneration(t, "gen-prov-1", plain, baseTime)
	generationAfter := runtimeTestGeneration(t, "gen-prov-2", annotated, baseTime.Add(time.Hour))
	// The payload bytes must differ, or this test would not prove the
	// semantic-checksum comparison over a raw payload-checksum comparison.
	require.NotEqual(t, generationBefore.Manifest.Payload.Checksum, generationAfter.Manifest.Payload.Checksum)
	require.NoError(t, generations.Commit(ctx, generationBefore, ""))
	require.NoError(t, generations.Commit(ctx, generationAfter, "gen-prov-1"))

	snapshot := newRoutableSnapshot(runtimeTestState(t, generationAfter), 1, nil)
	service := NewFreshnessService(staticSnapshotSource{snapshot}, generations)

	diff, err := service.Changes(ctx)
	require.NoError(t, err)
	assert.True(t, diff.Available)
	assert.True(t, diff.SemanticallyEqual)
	assert.Empty(t, diff.ModelsAdded)
	assert.Empty(t, diff.ModelsRemoved)
	assert.Empty(t, diff.OfferingsAdded)
	assert.Empty(t, diff.OfferingsRemoved)
	assert.Empty(t, diff.PriceChanges)
}
