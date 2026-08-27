package view

import (
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// A document page is the one unit this gateway bills that no token price
// describes. An offering that reads documents may publish a page price and
// nothing else, so the projection cannot decide from the token block alone
// whether an offering has a price worth reporting.

func pricePtr(value float64) *float64 { return &value }

// TestAnOfferingPricedOnlyByThePageStillReportsAPrice is the case the token
// block used to swallow. A recognition offering that published no token price
// projected no pricing at all, so a reader saw a document reader with no price
// and could not tell it from a free one.
func TestAnOfferingPricedOnlyByThePageStillReportsAPrice(t *testing.T) {
	pricing := offeringPricing(&catalogs.ModelPricing{
		Currency:   catalogs.ModelPricingCurrencyUSD,
		Operations: &catalogs.ModelOperationPricing{PageInput: pricePtr(0.001)},
	})

	require.NotNil(t, pricing, "an offering that prices a page reported no price")
	require.Equal(t, "0.001", pricing.PageInput)
	require.Equal(t, "USD", pricing.Currency)
	require.Empty(t, pricing.Prompt, "a page price was reported as a token price")
}

// TestAPagePriceTravelsBesideTheTokenPrices holds the offering that does both.
// A chat model that also reads documents publishes two unlike prices, and
// reporting either one alone leaves a caller unable to price half its traffic.
func TestAPagePriceTravelsBesideTheTokenPrices(t *testing.T) {
	pricing := offeringPricing(&catalogs.ModelPricing{
		Currency: catalogs.ModelPricingCurrencyUSD,
		Tokens: &catalogs.ModelTokenPricing{
			Input: &catalogs.ModelTokenCost{Per1M: 2},
		},
		Operations: &catalogs.ModelOperationPricing{PageInput: pricePtr(0.0000774)},
	})

	require.NotNil(t, pricing)
	require.Equal(t, "2e-06", pricing.Prompt)
	require.Equal(t, "7.74e-05", pricing.PageInput)
}

// TestAnOfferingThatPricesNothingReportsNothing keeps the absent case absent.
// An empty price block would read as a set of zero prices, which is the one
// answer a caller must never get from a catalog that published no number.
func TestAnOfferingThatPricesNothingReportsNothing(t *testing.T) {
	require.Nil(t, offeringPricing(nil))
	require.Nil(t, offeringPricing(&catalogs.ModelPricing{
		Currency:   catalogs.ModelPricingCurrencyUSD,
		Operations: &catalogs.ModelOperationPricing{},
	}))
}
