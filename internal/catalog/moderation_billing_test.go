package catalog

import (
	"math"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestModerationValuation(t *testing.T) {
	base := func() catalogs.ProviderOffering {
		return catalogs.ProviderOffering{
			Billing: &catalogs.ModelBilling{Moderations: &catalogs.ModerationBilling{Basis: catalogs.ModerationBillingRequests}},
			Pricing: &catalogs.ModelPricing{Currency: "USD", Operations: &catalogs.ModelOperationPricing{Request: new(0.0)}},
		}
	}
	for _, price := range []float64{0, 0.001} {
		offering := base()
		offering.Pricing.Operations.Request = &price
		v, err := ModerationValuation(offering, time.Now())
		require.NoError(t, err)
		amount, err := v.NanoUSD(reservation.Quantities{"request": 1})
		require.NoError(t, err)
		require.Equal(t, int64(price*1e9), amount)
	}
	for _, tc := range []struct {
		name   string
		change func(*catalogs.ProviderOffering)
	}{
		{"missing contract", func(o *catalogs.ProviderOffering) { o.Billing = nil }},
		{"wrong basis", func(o *catalogs.ProviderOffering) { o.Billing.Moderations.Basis = "tokens" }},
		{"missing pricing", func(o *catalogs.ProviderOffering) { o.Pricing = nil }},
		{"missing price", func(o *catalogs.ProviderOffering) { o.Pricing.Operations.Request = nil }},
		{"negative", func(o *catalogs.ProviderOffering) { o.Pricing.Operations.Request = new(-1.0) }},
		{"nan", func(o *catalogs.ProviderOffering) { o.Pricing.Operations.Request = new(math.NaN()) }},
		{"infinite", func(o *catalogs.ProviderOffering) { o.Pricing.Operations.Request = new(math.Inf(1)) }},
		{"currency", func(o *catalogs.ProviderOffering) { o.Pricing.Currency = "EUR" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base()
			tc.change(&o)
			_, err := ModerationValuation(o, time.Now())
			require.ErrorIs(t, err, reservation.ErrValuation)
		})
	}
}
