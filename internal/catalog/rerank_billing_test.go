package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestRerankValuation(t *testing.T) {
	base := func() catalogs.ProviderOffering {
		return catalogs.ProviderOffering{
			Billing: &catalogs.ModelBilling{Rerank: &catalogs.RerankBilling{Basis: catalogs.RerankBillingQueryDocumentTokens, RequestCharge: new(false)}},
			Pricing: &catalogs.ModelPricing{Currency: "USD", Operations: &catalogs.ModelOperationPricing{RerankBasis: catalogs.ModelRerankBasisToken}, Tokens: &catalogs.ModelTokenPricing{Input: &catalogs.ModelTokenCost{PerToken: 0, Per1M: 0.02}}},
		}
	}
	offering := base()
	valuation, err := RerankValuation(offering, time.Now())
	require.NoError(t, err)
	amount, err := valuation.NanoUSD(reservation.Quantities{"input": 8})
	require.NoError(t, err)
	require.EqualValues(t, 160, amount)
	for _, test := range []struct {
		name   string
		change func(*catalogs.ProviderOffering)
	}{
		{"conflicting legacy basis", func(o *catalogs.ProviderOffering) {
			o.Pricing.Operations.RerankBasis = catalogs.ModelRerankBasisSearchUnit
		}},
		{"absent contract", func(o *catalogs.ProviderOffering) { o.Billing = nil }},
		{"missing price", func(o *catalogs.ProviderOffering) { o.Pricing.Tokens.Input = nil }},
		{"unknown price", func(o *catalogs.ProviderOffering) {
			o.Pricing.Tokens.Input.SetAmountUnknown(catalogs.CostUnitPerMillion)
		}},
		{"unknown currency", func(o *catalogs.ProviderOffering) { o.Pricing.Currency = "EUR" }},
		{"unpriced request", func(o *catalogs.ProviderOffering) { *o.Billing.Rerank.RequestCharge = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := base()
			test.change(&o)
			_, err := RerankValuation(o, time.Now())
			require.ErrorIs(t, err, reservation.ErrValuation)
		})
	}
	offering = base()
	offering.Pricing.Tokens.Input.SetAmount(catalogs.CostUnitPerMillion, 0)
	valuation, err = RerankValuation(offering, time.Now())
	require.NoError(t, err)
	amount, err = valuation.NanoUSD(reservation.Quantities{"input": 8})
	require.NoError(t, err)
	require.Zero(t, amount)
}
