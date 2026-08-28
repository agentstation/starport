package catalog

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// Two providers bill reranking in two different units. Cohere charges a search
// unit, which is one query against a bounded document count. Voyage charges the
// tokens it reads. The offering names its own basis, so the rule below reads the
// price that basis points at and nothing else.

// rerankPrice is one offering's published rerank price, stated the way a
// catalog states it: a basis, and the price that basis points at.
type rerankPrice struct {
	basis      catalogs.ModelRerankBasis
	searchUnit *float64
	perToken   *float64
}

// rerankPricing builds the pricing block one rerank offering publishes.
func rerankPricing(price rerankPrice) *catalogs.ModelPricing {
	pricing := &catalogs.ModelPricing{Currency: catalogs.ModelPricingCurrencyUSD}
	if price.basis != "" || price.searchUnit != nil {
		pricing.Operations = &catalogs.ModelOperationPricing{
			RerankBasis: price.basis,
			SearchUnit:  price.searchUnit,
		}
	}
	if price.perToken != nil {
		pricing.Tokens = &catalogs.ModelTokenPricing{
			Input: &catalogs.ModelTokenCost{PerToken: *price.perToken},
		}
	}
	return pricing
}

// rerankOffering builds one offering that serves reranking beside chat. Two
// operations rather than one is deliberate: an unpriced rerank price is a
// statement about reranking, and the chat operation has to survive it.
func rerankOffering(price rerankPrice) catalogs.ProviderOffering {
	return catalogs.ProviderOffering{
		ProviderID:      "cohere",
		ProviderModelID: "rerank-v3.5",
		DefinitionID:    "cohere/rerank-v3.5",
		Pricing:         rerankPricing(price),
		Service: catalogs.ProviderOfferingServiceCapabilities{
			Operations: []catalogs.ProviderOperation{
				catalogs.ProviderOperationChatCompletions,
				catalogs.ProviderOperationRerank,
			},
		},
		Endpoints: []catalogs.ProviderOfferingEndpoint{
			{
				Operation: catalogs.ProviderOperationChatCompletions,
				Type:      "cohere",
				URL:       "https://example.invalid/v2/chat",
			},
			{
				Operation: catalogs.ProviderOperationRerank,
				Type:      "cohere",
				URL:       "https://example.invalid/v2/rerank",
			},
		},
	}
}

func rerankAdapter() AdapterAvailability {
	return AdapterAvailability{
		ProviderID: "cohere",
		Registered: true,
		Operations: []catalogs.ProviderOperation{
			catalogs.ProviderOperationChatCompletions,
			catalogs.ProviderOperationRerank,
		},
		EndpointTypes: []catalogs.EndpointType{"cohere"},
	}
}

// TestTheBasisDecidesWhichRerankPriceIsRead is the whole rule in one table. An
// offering that bills a search unit answers from the search unit price, and one
// that bills tokens answers from the token price. Reading whichever price
// happens to be present would bill a Voyage turn at a Cohere rate.
func TestTheBasisDecidesWhichRerankPriceIsRead(t *testing.T) {
	for _, test := range []struct {
		name     string
		price    rerankPrice
		billable bool
	}{
		{
			name:     "a search unit basis with its own price",
			price:    rerankPrice{basis: catalogs.ModelRerankBasisSearchUnit, searchUnit: priceOf(0.001)},
			billable: true,
		},
		{
			name:     "a token basis with its own price",
			price:    rerankPrice{basis: catalogs.ModelRerankBasisToken, perToken: priceOf(0.00000005)},
			billable: true,
		},
		{
			name: "a search unit basis priced only in tokens",
			price: rerankPrice{
				basis:    catalogs.ModelRerankBasisSearchUnit,
				perToken: priceOf(0.00000005),
			},
		},
		{
			name: "a token basis priced only in search units",
			price: rerankPrice{
				basis:      catalogs.ModelRerankBasisToken,
				searchUnit: priceOf(0.001),
			},
		},
		{
			// The basis exists so a consumer reads the right price rather than
			// guessing from the one that is present. An offering that states no
			// basis has not said which of its prices reranking draws from.
			name:  "no basis at all, beside a token price",
			price: rerankPrice{perToken: priceOf(0.00000005)},
		},
		{
			name:  "a negative search unit price",
			price: rerankPrice{basis: catalogs.ModelRerankBasisSearchUnit, searchUnit: priceOf(-0.001)},
		},
		{
			name:  "no prices at all",
			price: rerankPrice{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := billableOperation(rerankOffering(test.price), catalogs.ProviderOperationRerank)
			if test.billable {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrRerankUnpriced)
		})
	}
}

// TestAFreeRerankOfferingIsBillable separates two facts that both read as "no
// charge". A stated price of zero is a provider decision. A missing price is a
// catalog gap, and only the catalog can tell the two apart.
func TestAFreeRerankOfferingIsBillable(t *testing.T) {
	require.NoError(t, billableOperation(
		rerankOffering(rerankPrice{
			basis:      catalogs.ModelRerankBasisSearchUnit,
			searchUnit: priceOf(0),
		}),
		catalogs.ProviderOperationRerank,
	))
}

// TestAnUnpricedRerankOfferingLosesOnlyTheRerankOperation is decision RNK-D7 in
// the projection. The gateway would otherwise answer a rerank request, pay the
// provider for it, and record nothing against the account that asked.
//
// The chat operation is deliberately untouched. A missing rerank price says
// nothing about chat, and dropping the whole offering would refuse a caller who
// never asked to rerank.
func TestAnUnpricedRerankOfferingLosesOnlyTheRerankOperation(t *testing.T) {
	operations, endpoints, unpriced := compatibleOfferingService(
		rerankAdapter(),
		rerankOffering(rerankPrice{}),
	)

	require.True(t, unpriced)
	require.Equal(t, []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, operations)
	require.Len(t, endpoints, 1)

	// The priced offering keeps both, which is the case the shipped catalog
	// produces. A rule that dropped reranking either way would pass the
	// assertion above and route nothing.
	operations, endpoints, unpriced = compatibleOfferingService(
		rerankAdapter(),
		rerankOffering(rerankPrice{
			basis:      catalogs.ModelRerankBasisSearchUnit,
			searchUnit: priceOf(0.001),
		}),
	)
	require.False(t, unpriced)
	require.Equal(t, []catalogs.ProviderOperation{
		catalogs.ProviderOperationChatCompletions,
		catalogs.ProviderOperationRerank,
	}, operations)
	require.Len(t, endpoints, 2)
}

// TestTheShippedCatalogPricesEveryRerankRoute reads the real generation. RNK2
// wrote these offerings in Starmap, and this is the assertion that both the
// basis and the price it points at reached Starport rather than being dropped
// somewhere between the two repositories.
func TestTheShippedCatalogPricesEveryRerankRoute(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	source := client.Catalog()

	bases := map[catalogs.ModelRerankBasis]int{}
	for _, provider := range source.Providers().List() {
		offerings, err := source.ProviderOfferings(provider.ID)
		require.NoError(t, err)
		for _, offering := range offerings {
			if !offering.Supports(catalogs.ProviderOperationRerank) {
				continue
			}
			require.NoErrorf(t,
				billableOperation(offering, catalogs.ProviderOperationRerank),
				"%s/%s", provider.ID, offering.ProviderModelID,
			)
			require.NotNil(t, offering.Limits)
			require.Positivef(t, offering.Limits.MaxDocuments,
				"%s/%s states no document limit", provider.ID, offering.ProviderModelID)
			bases[offering.Pricing.Operations.RerankBasis]++
		}
	}

	// The census RNK2 records. Both bases have to stay populated: a generation
	// that lost the token-billed offerings would still pass every assertion
	// above and would quietly reduce reranking to one billing model.
	require.Equal(t, map[catalogs.ModelRerankBasis]int{
		catalogs.ModelRerankBasisSearchUnit: 3,
		catalogs.ModelRerankBasisToken:      2,
	}, bases)
}

// searchUnitSnapshot projects one generation whose providers all serve
// reranking at the given prices, keyed by provider ID.
func searchUnitSnapshot(t *testing.T, prices map[string]rerankPrice) *RoutableSnapshot {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "author", Name: "Author"}))
	modalities := catalogs.ModelModalities{
		Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
		Output: []catalogs.ModelModality{catalogs.ModelModalityText},
	}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{
		ID: "rerank", Name: "Rerank",
		Authors:  []catalogs.Author{{ID: "author", Name: "Author"}},
		Features: &catalogs.ModelFeatures{Modalities: modalities},
	}))
	routes := make([]Route, 0, len(prices))
	for providerID, price := range prices {
		require.NoError(t, builder.SetProvider(catalogs.Provider{
			ID: catalogs.ProviderID(providerID), Name: providerID,
			Inference: &catalogs.ProviderInference{
				BaseURL: "https://example.invalid",
				Endpoints: []catalogs.ProviderInferenceEndpoint{{
					Operation: catalogs.ProviderOperationRerank,
					Type:      "cohere",
					Path:      "/v2/rerank",
				}},
			},
			Models: map[string]*catalogs.Model{"opaque/rerank@001": {
				ID: "opaque/rerank@001", ModelRef: "author/rerank", Name: "Provider model",
				Status:   catalogs.ModelStatusActive,
				Features: &catalogs.ModelFeatures{Modalities: modalities},
				Pricing:  rerankPricing(price),
			}},
		}))
		routes = append(routes, Route{
			DefinitionID:    "author/rerank",
			ProviderID:      catalogs.ProviderID(providerID),
			ProviderModelID: "opaque/rerank@001",
			Operations:      []catalogs.ProviderOperation{catalogs.ProviderOperationRerank},
		})
	}
	catalog, err := builder.Build()
	require.NoError(t, err)
	return newRoutableSnapshot(starmap.CatalogState{Catalog: catalog}, 0, routes, nil)
}

// TestTheLowestSearchUnitPriceIsTheCheapestOfferingOfThatModel is the bound a
// spend budget reads before a route exists. The planner has not chosen yet, so
// the cheapest published price is the only floor that refuses no call the
// account could have paid for.
func TestTheLowestSearchUnitPriceIsTheCheapestOfferingOfThatModel(t *testing.T) {
	snapshot := searchUnitSnapshot(t, map[string]rerankPrice{
		"dear-provider":  {basis: catalogs.ModelRerankBasisSearchUnit, searchUnit: priceOf(0.0025)},
		"cheap-provider": {basis: catalogs.ModelRerankBasisSearchUnit, searchUnit: priceOf(0.001)},
	})

	lowest, priced := snapshot.LowestSearchUnitPrice("author/rerank")
	require.True(t, priced)
	require.InDelta(t, 0.001, lowest, 1e-12,
		"a spend bound refused a call the cheapest offering could have served")

	// The provider-qualified name answers too, because a caller that named one
	// offering is asking about the same model.
	lowest, priced = snapshot.LowestSearchUnitPrice("dear-provider/opaque/rerank@001")
	require.True(t, priced)
	require.InDelta(t, 0.0025, lowest, 1e-12)
}

// TestATokenBilledRerankOfferingStatesNoFloor holds where the estimate stops. A
// provider that bills the tokens it reads has read nothing yet, so no number
// here is a floor, and inventing one would refuse a call over a price the
// account was never going to be charged.
func TestATokenBilledRerankOfferingStatesNoFloor(t *testing.T) {
	snapshot := searchUnitSnapshot(t, map[string]rerankPrice{
		"voyage": {basis: catalogs.ModelRerankBasisToken, perToken: priceOf(0.00000005)},
	})

	_, priced := snapshot.LowestSearchUnitPrice("author/rerank")
	require.False(t, priced, "a token-billed offering reported a search unit price")

	_, priced = snapshot.LowestSearchUnitPrice("author/absent")
	require.False(t, priced)

	// Every other accessor on this type tolerates a nil receiver, and a price
	// that panicked would crash the request that lost its catalog.
	var absent *RoutableSnapshot
	_, priced = absent.LowestSearchUnitPrice("author/rerank")
	require.False(t, priced)
}
