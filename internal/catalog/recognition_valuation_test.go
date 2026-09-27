package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestRecognitionValuationCompleteUnits(t *testing.T) {
	token := textPricedOffering()
	text := token.Billing.TextChat
	token.Billing = &catalogs.ModelBilling{Recognition: &catalogs.RecognitionBilling{Basis: catalogs.RecognitionBillingTokens, RequestCharge: new(false), Input: text.Input, Output: text.Output}}
	valuation, err := RecognitionValuation(token, time.Now())
	require.NoError(t, err)
	cost, err := valuation.NanoUSD(reservation.Quantities{"input": 4, "cache_read": 4, "output": 3})
	require.NoError(t, err)
	require.EqualValues(t, 2700, cost)
	require.Nil(t, token.Billing.TextChat, "recognition pricing must not change the retained offering")
	for _, test := range []struct {
		name          string
		page, request float64
		charged       bool
		want          int64
	}{
		{"pages", 0.001, 99, false, 2000000},
		{"pages and request", 0.001, 0.002, true, 4000000},
		{"explicit free", 0, 0, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			offering := recognitionOffering(new(test.page))
			offering.Billing.Recognition.RequestCharge = new(test.charged)
			offering.Pricing.Operations.Request = new(test.request)
			valuation, err := RecognitionValuation(offering, time.Now())
			require.NoError(t, err)
			units := reservation.Quantities{"page": 2}
			if test.charged {
				units["request"] = 1
			}
			cost, err := valuation.NanoUSD(units)
			require.NoError(t, err)
			require.Equal(t, test.want, cost)
		})
	}
	for _, test := range []struct {
		name   string
		change func(*catalogs.ProviderOffering)
	}{
		{"unknown contract", func(o *catalogs.ProviderOffering) { o.Billing = nil }},
		{"unknown request charge", func(o *catalogs.ProviderOffering) { o.Billing.Recognition.RequestCharge = nil }},
		{"missing page rate", func(o *catalogs.ProviderOffering) { o.Pricing.Operations.PageInput = nil }},
		{"missing request rate", func(o *catalogs.ProviderOffering) { o.Billing.Recognition.RequestCharge = new(true) }},
		{"non USD", func(o *catalogs.ProviderOffering) { o.Pricing.Currency = "EUR" }},
		{"unresolved tier", func(o *catalogs.ProviderOffering) {
			o.Pricing.Tiers = []catalogs.ModelPricingTier{{Type: catalogs.ModelPricingTierTypeContext, Size: 100}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			offering := recognitionOffering(new(0.001))
			offering.Billing.Recognition.RequestCharge = new(false)
			test.change(&offering)
			_, err := RecognitionValuation(offering, time.Now())
			require.ErrorIs(t, err, reservation.ErrValuation)
		})
	}
}
