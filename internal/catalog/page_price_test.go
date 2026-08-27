package catalog

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// A page is the unit recognition is billed in. No token price converts into it,
// so a caller that wants to know what reading a document costs has exactly one
// place to ask, and these are the two questions it asks: what does the offering
// the planner chose charge, and what is the least any offering charges?

// pagePricedCatalog builds a generation whose providers serve document
// recognition at the given per-page prices, keyed by provider ID.
func pagePricedCatalog(t *testing.T, prices map[string]*float64) *catalogs.Catalog {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "author", Name: "Author"}))
	modalities := catalogs.ModelModalities{
		Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
		Output: []catalogs.ModelModality{catalogs.ModelModalityText},
	}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{
		ID: "model", Name: "Model",
		Authors:  []catalogs.Author{{ID: "author", Name: "Author"}},
		Features: &catalogs.ModelFeatures{Modalities: modalities},
	}))
	for providerID, page := range prices {
		pricing := &catalogs.ModelPricing{Currency: catalogs.ModelPricingCurrencyUSD}
		if page != nil {
			pricing.Operations = &catalogs.ModelOperationPricing{PageInput: page}
		}
		require.NoError(t, builder.SetProvider(catalogs.Provider{
			ID: catalogs.ProviderID(providerID), Name: providerID,
			Inference: &catalogs.ProviderInference{
				BaseURL: "https://example.invalid",
				Endpoints: []catalogs.ProviderInferenceEndpoint{{
					Operation: catalogs.ProviderOperationDocumentsRecognition,
					Type:      "openai-chat-completions",
					Path:      "/v1/chat/completions",
				}},
			},
			Models: map[string]*catalogs.Model{"opaque/model@001": {
				ID: "opaque/model@001", ModelRef: "author/model", Name: "Provider model",
				Status:   catalogs.ModelStatusActive,
				Features: &catalogs.ModelFeatures{Modalities: modalities},
				Pricing:  pricing,
			}},
		}))
	}
	catalog, err := builder.Build()
	require.NoError(t, err)
	return catalog
}

// pagePricedSnapshot projects that generation into a snapshot whose routes all
// serve recognition.
func pagePricedSnapshot(t *testing.T, prices map[string]*float64) *RoutableSnapshot {
	t.Helper()
	routes := make([]Route, 0, len(prices))
	for providerID := range prices {
		routes = append(routes, Route{
			DefinitionID:    "author/model",
			ProviderID:      catalogs.ProviderID(providerID),
			ProviderModelID: "opaque/model@001",
			Operations: []catalogs.ProviderOperation{
				catalogs.ProviderOperationDocumentsRecognition,
			},
		})
	}
	catalog := pagePricedCatalog(t, prices)
	return newRoutableSnapshot(starmap.CatalogState{Catalog: catalog}, 0, routes, nil)
}

// TestAModelsPagePriceIsTheOneItsOfferingPublishes is the price the meter
// charges after the planner has chosen. It is the exact offering's own number,
// because that is the offering the provider will bill for.
func TestAModelsPagePriceIsTheOneItsOfferingPublishes(t *testing.T) {
	snapshot := pagePricedSnapshot(t, map[string]*float64{
		"cheap-provider": priceOf(0.0000774),
		"dear-provider":  priceOf(0.004),
	})

	price, priced := snapshot.PagePriceFor(
		"dear-provider/opaque/model@001",
		catalogs.ProviderOperationDocumentsRecognition,
	)
	require.True(t, priced)
	require.InDelta(t, 0.004, price, 1e-12,
		"a document was billed at a price its own offering does not publish")
}

// TestAModelThatServesNoRecognitionHasNoPagePrice states the operation bound.
// An offering priced for chat is not priced for reading a document, and
// answering with any number here would invent one.
func TestAModelThatServesNoRecognitionHasNoPagePrice(t *testing.T) {
	snapshot := pagePricedSnapshot(t, map[string]*float64{"cheap-provider": priceOf(0.001)})

	_, priced := snapshot.PagePriceFor(
		"cheap-provider/opaque/model@001",
		catalogs.ProviderOperationChatCompletions,
	)
	require.False(t, priced)

	_, priced = snapshot.PagePriceFor(
		"absent-provider/opaque/model@001",
		catalogs.ProviderOperationDocumentsRecognition,
	)
	require.False(t, priced)
}

// TestTheLowestPagePriceIsTheCheapestOfferingInTheGeneration is the bound a
// caller reads before a route exists.
//
// A spend budget has to refuse a document before the planner has chosen the
// model that will read it. The cheapest published price is the only bound that
// refuses no work the account could have paid for.
func TestTheLowestPagePriceIsTheCheapestOfferingInTheGeneration(t *testing.T) {
	snapshot := pagePricedSnapshot(t, map[string]*float64{
		"dear-provider":  priceOf(0.004),
		"cheap-provider": priceOf(0.0000774),
		"mid-provider":   priceOf(0.0009),
	})

	lowest, priced := snapshot.LowestPagePrice(catalogs.ProviderOperationDocumentsRecognition)
	require.True(t, priced)
	require.InDelta(t, 0.0000774, lowest, 1e-12,
		"a spend bound refused a document the cheapest offering could have read")
}

// TestAGenerationThatPricesNoPageAnswersNothing holds how the lookup fails.
//
// The projection drops a recognition offering that publishes no page price, so
// this state means the gateway has no priced reader at all. Answering zero
// would read as a free page and let an unpriced document through every bound.
func TestAGenerationThatPricesNoPageAnswersNothing(t *testing.T) {
	snapshot := pagePricedSnapshot(t, map[string]*float64{"cheap-provider": nil})

	_, priced := snapshot.LowestPagePrice(catalogs.ProviderOperationDocumentsRecognition)
	require.False(t, priced, "an unpriced page was reported as costing nothing")

	_, priced = snapshot.PagePriceFor(
		"cheap-provider/opaque/model@001",
		catalogs.ProviderOperationDocumentsRecognition,
	)
	require.False(t, priced)
}

// TestAnAbsentSnapshotPricesNothing states the nil receiver. Every other
// accessor on this type tolerates one, and a price that panicked where a
// routing lookup returns cleanly would crash the request that lost its catalog.
func TestAnAbsentSnapshotPricesNothing(t *testing.T) {
	var snapshot *RoutableSnapshot
	_, priced := snapshot.LowestPagePrice(catalogs.ProviderOperationDocumentsRecognition)
	require.False(t, priced)
	_, priced = snapshot.PagePriceFor("any/model", catalogs.ProviderOperationDocumentsRecognition)
	require.False(t, priced)
}
