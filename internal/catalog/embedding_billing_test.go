package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingValuation(t *testing.T) {
	base := func() catalogs.ProviderOffering {
		return catalogs.ProviderOffering{
			Billing: &catalogs.ModelBilling{Embeddings: &catalogs.EmbeddingBilling{Basis: catalogs.EmbeddingBillingInputTokens, RequestCharge: new(false)}},
			Pricing: &catalogs.ModelPricing{Currency: "USD", Tokens: &catalogs.ModelTokenPricing{Input: &catalogs.ModelTokenCost{PerToken: 0, Per1M: 0.02}}},
		}
	}
	offering := base()
	valuation, err := EmbeddingValuation(offering, time.Now())
	require.NoError(t, err)
	amount, err := valuation.NanoUSD(reservation.Quantities{"input": 8})
	require.NoError(t, err)
	require.EqualValues(t, 160, amount)
	for _, test := range []struct {
		name   string
		change func(*catalogs.ProviderOffering)
	}{
		{"absent contract", func(o *catalogs.ProviderOffering) { o.Billing = nil }},
		{"missing price", func(o *catalogs.ProviderOffering) { o.Pricing.Tokens.Input = nil }},
		{"unknown price", func(o *catalogs.ProviderOffering) {
			o.Pricing.Tokens.Input.SetAmountUnknown(catalogs.CostUnitPerMillion)
		}},
		{"unknown currency", func(o *catalogs.ProviderOffering) { o.Pricing.Currency = "EUR" }},
		{"unpriced request", func(o *catalogs.ProviderOffering) { *o.Billing.Embeddings.RequestCharge = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := base()
			test.change(&o)
			_, err := EmbeddingValuation(o, time.Now())
			require.ErrorIs(t, err, reservation.ErrValuation)
		})
	}
	offering = base()
	offering.Pricing.Tokens.Input.SetAmount(catalogs.CostUnitPerMillion, 0)
	valuation, err = EmbeddingValuation(offering, time.Now())
	require.NoError(t, err)
	amount, err = valuation.NanoUSD(reservation.Quantities{"input": 8})
	require.NoError(t, err)
	require.Zero(t, amount)
}
