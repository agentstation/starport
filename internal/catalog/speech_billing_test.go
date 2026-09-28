package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestSpeechValuation(t *testing.T) {
	base := func() catalogs.ProviderOffering {
		return catalogs.ProviderOffering{Billing: &catalogs.ModelBilling{Speech: &catalogs.SpeechBilling{Basis: catalogs.SpeechBillingCodePoints, MaxInputCharacters: 4096, RequestCharge: new(false)}}, Pricing: &catalogs.ModelPricing{Currency: "USD", Operations: &catalogs.ModelOperationPricing{CharacterInput: new(0.000015)}}}
	}
	offering := base()
	v, err := SpeechValuation(offering, time.Now())
	require.NoError(t, err)
	amount, err := v.NanoUSD(reservation.Quantities{SpeechCharacterUnit: 5})
	require.NoError(t, err)
	require.EqualValues(t, 75000, amount)
	for _, tc := range []struct {
		name   string
		change func(*catalogs.ProviderOffering)
	}{
		{"missing contract", func(o *catalogs.ProviderOffering) { o.Billing = nil }},
		{"missing price", func(o *catalogs.ProviderOffering) { o.Pricing.Operations.CharacterInput = nil }},
		{"wrong currency", func(o *catalogs.ProviderOffering) { o.Pricing.Currency = "EUR" }},
		{"missing request fee", func(o *catalogs.ProviderOffering) { *o.Billing.Speech.RequestCharge = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base()
			tc.change(&o)
			_, err := SpeechValuation(o, time.Now())
			require.ErrorIs(t, err, reservation.ErrValuation)
		})
	}
	offering = base()
	*offering.Billing.Speech.RequestCharge = true
	offering.Pricing.Operations.Request = new(0.001)
	v, err = SpeechValuation(offering, time.Now())
	require.NoError(t, err)
	amount, err = v.NanoUSD(reservation.Quantities{SpeechCharacterUnit: 5, "request": 1})
	require.NoError(t, err)
	require.EqualValues(t, 1075000, amount)
	offering = base()
	offering.Pricing.Operations.CharacterInput = new(0.0)
	v, err = SpeechValuation(offering, time.Now())
	require.NoError(t, err)
	amount, err = v.NanoUSD(reservation.Quantities{SpeechCharacterUnit: 5})
	require.NoError(t, err)
	require.Zero(t, amount)
}
