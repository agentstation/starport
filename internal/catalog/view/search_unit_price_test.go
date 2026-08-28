package view

import (
	"slices"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// A rerank offering bills either a search unit or the tokens it reads, and the
// two are not interchangeable. Cohere publishes a search unit price and no
// token price at all, so the projection that decides from the token block alone
// would report a priced reranker as one nobody priced.

// TestAnOfferingPricedOnlyBySearchUnitStillReportsAPrice is the console-facing
// half of decision RNK-D7. A row with four empty token columns and no unit
// price reads as a free model, which is the one answer the catalog never gave.
func TestAnOfferingPricedOnlyBySearchUnitStillReportsAPrice(t *testing.T) {
	pricing := offeringPricing(&catalogs.ModelPricing{
		Currency: catalogs.ModelPricingCurrencyUSD,
		Operations: &catalogs.ModelOperationPricing{
			RerankBasis: catalogs.ModelRerankBasisSearchUnit,
			SearchUnit:  pricePtr(0.0025),
		},
	})

	require.NotNil(t, pricing, "an offering that prices a search unit reported no price")
	require.Equal(t, "0.0025", pricing.SearchUnit)
	require.Empty(t, pricing.Prompt, "a search unit price was reported as a token price")
	require.Empty(t, pricing.PageInput, "a search unit price was reported as a page price")
}

// TestTheBasisDecidesWhetherASearchUnitPriceIsReported carries the RNK8 meter
// rule out to the reader. Voyage bills the tokens it reads. A stale search unit
// figure beside that basis would price a turn in a unit the provider never
// charges, and the reader would take the smaller of the two numbers.
func TestTheBasisDecidesWhetherASearchUnitPriceIsReported(t *testing.T) {
	pricing := offeringPricing(&catalogs.ModelPricing{
		Currency: catalogs.ModelPricingCurrencyUSD,
		Tokens: &catalogs.ModelTokenPricing{
			Input: &catalogs.ModelTokenCost{Per1M: 2},
		},
		Operations: &catalogs.ModelOperationPricing{
			RerankBasis: catalogs.ModelRerankBasisToken,
			SearchUnit:  pricePtr(0.0025),
		},
	})

	require.NotNil(t, pricing)
	require.Equal(t, "2e-06", pricing.Prompt)
	require.Empty(t, pricing.SearchUnit,
		"a token-billed offering reported a price in a unit it never bills")

	// An offering that states no basis states no rerank price either, which is
	// the same rule the control plane holds before planning.
	unstated := offeringPricing(&catalogs.ModelPricing{
		Currency:   catalogs.ModelPricingCurrencyUSD,
		Operations: &catalogs.ModelOperationPricing{SearchUnit: pricePtr(0.0025)},
	})
	require.Nil(t, unstated)
}

// TestTheDocumentLimitTravelsWithTheOffering holds the one bound a rerank
// caller has to plan around. Sending more documents than the provider ranks is
// a refusal, and the count belongs beside the context window rather than inside
// the price block, because it is a shape rather than a cost.
//
// The fixture is the shipped catalog rather than a hand-built offering, so the
// projection cannot drift from what the console is actually served.
func TestTheDocumentLimitTravelsWithTheOffering(t *testing.T) {
	snapshot := fixtureSnapshot(t, "cohere")
	var reranked int
	for _, model := range Models(snapshot) {
		for _, offering := range model.Offerings {
			if !slices.Contains(offering.Operations, string(catalogs.ProviderOperationRerank)) {
				continue
			}
			reranked++
			require.NotNilf(t, offering.MaxDocuments,
				"rerank offering %s/%s ranks an unstated number of documents",
				offering.Provider, offering.ProviderModelID)
			require.Positive(t, *offering.MaxDocuments)
			require.NotNilf(t, offering.Pricing,
				"rerank offering %s/%s reported no price at all",
				offering.Provider, offering.ProviderModelID)
			require.NotEmpty(t, offering.Pricing.SearchUnit)
		}
	}
	require.NotZero(t, reranked, "cohere serves reranking in the shipped catalog")

	// A chat offering states no document limit, so the console renders the
	// column empty rather than borrowing another offering's bound.
	chat := fixtureSnapshot(t, "anthropic")
	for _, model := range Models(chat) {
		for _, offering := range model.Offerings {
			require.Nil(t, offering.MaxDocuments)
		}
	}
}
