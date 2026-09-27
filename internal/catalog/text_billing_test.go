package catalog

import (
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func textPricedOffering() catalogs.ProviderOffering {
	return catalogs.ProviderOffering{
		Billing: &catalogs.ModelBilling{TextChat: &catalogs.TextChatBilling{Input: []catalogs.TokenBillingClass{catalogs.TokenBillingInput, catalogs.TokenBillingCacheRead}, Output: []catalogs.TokenBillingClass{catalogs.TokenBillingOutput}, RequestCharge: new(false)}},
		Pricing: &catalogs.ModelPricing{Currency: "USD", Tokens: &catalogs.ModelTokenPricing{Input: &catalogs.ModelTokenCost{Per1M: 0.15}, CacheRead: &catalogs.ModelTokenCost{Per1M: 0.075}, Output: &catalogs.ModelTokenCost{Per1M: 0.6}}},
	}
}

func TestTextChatValuationUsesCompleteCatalogContract(t *testing.T) {
	original := textPricedOffering()
	valuation, err := TextChatValuation(original, time.Now())
	require.NoError(t, err)
	original.Pricing.Tokens.Input.Per1M = 99
	actual, err := valuation.NanoUSD(reservation.Quantities{"input": 4, "cache_read": 4, "output": 3})
	require.NoError(t, err)
	require.EqualValues(t, 2700, actual, "valuation retains exact original decimal rates")
	for _, test := range []struct {
		name   string
		change func(*catalogs.ProviderOffering)
	}{
		{"absent declaration", func(o *catalogs.ProviderOffering) { o.Billing = nil }},
		{"incomplete declaration", func(o *catalogs.ProviderOffering) { o.Billing.TextChat.RequestCharge = nil }},
		{"absent cache price", func(o *catalogs.ProviderOffering) { o.Pricing.Tokens.CacheRead = nil }},
		{"non-USD", func(o *catalogs.ProviderOffering) { o.Pricing.Currency = "EUR" }},
		{"unknown price", func(o *catalogs.ProviderOffering) {
			o.Pricing.Tokens.Input.SetAmountUnknown(catalogs.CostUnitPerToken)
			o.Pricing.Tokens.Input.SetAmountUnknown(catalogs.CostUnitPerMillion)
		}},
		{"request charge lacks price", func(o *catalogs.ProviderOffering) { *o.Billing.TextChat.RequestCharge = true }},
		{"unresolved tier", func(o *catalogs.ProviderOffering) {
			o.Pricing.Tiers = []catalogs.ModelPricingTier{{Type: catalogs.ModelPricingTierTypeContext, Size: 100, Tokens: &catalogs.ModelTokenPricing{Input: &catalogs.ModelTokenCost{Per1M: 1}}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := textPricedOffering()
			test.change(&o)
			_, err := TextChatValuation(o, time.Now())
			require.ErrorIs(t, err, reservation.ErrValuation)
		})
	}
	free := textPricedOffering()
	free.Pricing.Tokens = &catalogs.ModelTokenPricing{Input: &catalogs.ModelTokenCost{}, Output: &catalogs.ModelTokenCost{}, CacheRead: &catalogs.ModelTokenCost{}}
	valuation, err = TextChatValuation(free, time.Now())
	require.NoError(t, err)
	actual, err = valuation.NanoUSD(reservation.Quantities{"input": 4, "cache_read": 4, "output": 3})
	require.NoError(t, err)
	require.Zero(t, actual)
}
