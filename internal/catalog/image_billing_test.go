package catalog

import (
	"math"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestImageValuation(t *testing.T) {
	base := func() catalogs.ProviderOffering {
		return catalogs.ProviderOffering{Billing: &catalogs.ModelBilling{Images: &catalogs.ImageBilling{Basis: catalogs.ImageBillingPixelIterations, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationImagesGenerations}, DefaultImages: 1, DefaultWidth: 1024, DefaultHeight: 1024, Iterations: 1, UnitPixels: 1048576, UnitIterations: 1, RequestCharge: new(false)}}, Pricing: &catalogs.ModelPricing{Currency: "USD", Operations: &catalogs.ModelOperationPricing{ImageUnit: new(0.0005)}}}
	}
	for _, tc := range []struct {
		name, size  string
		count, cost int64
	}{
		{"explicit pixels", "1024x1536", 2, 1500000},
		{"defaults", "", 0, 500000},
		{"fractional price unit", "128x128", 1, 7813},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base()
			v, err := ImageValuation(o, catalogs.ProviderOperationImagesGenerations, time.Now())
			require.NoError(t, err)
			units, err := ImageQuantities(o.Billing.Images, tc.count, tc.size)
			require.NoError(t, err)
			amount, err := v.NanoUSD(units)
			require.NoError(t, err)
			require.Equal(t, tc.cost, amount)
		})
	}
	o := base()
	o.Billing.Images.Iterations = 5
	o.Billing.Images.UnitIterations = 25
	v, err := ImageValuation(o, catalogs.ProviderOperationImagesGenerations, time.Now())
	require.NoError(t, err)
	q, err := ImageQuantities(o.Billing.Images, 1, "")
	require.NoError(t, err)
	amount, err := v.NanoUSD(q)
	require.NoError(t, err)
	require.EqualValues(t, 100000, amount)
	for _, tc := range []struct {
		name   string
		change func(*catalogs.ProviderOffering)
	}{
		{"missing contract", func(o *catalogs.ProviderOffering) { o.Billing = nil }},
		{"missing unit price", func(o *catalogs.ProviderOffering) { o.Pricing.Operations.ImageUnit = nil }},
		{"legacy flat price", func(o *catalogs.ProviderOffering) {
			o.Pricing.Operations.ImageUnit = nil
			o.Pricing.Operations.ImageGen = new(0.0005)
		}},
		{"wrong currency", func(o *catalogs.ProviderOffering) { o.Pricing.Currency = "EUR" }},
		{"missing request fee", func(o *catalogs.ProviderOffering) { *o.Billing.Images.RequestCharge = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base()
			tc.change(&o)
			_, err := ImageValuation(o, catalogs.ProviderOperationImagesGenerations, time.Now())
			require.Error(t, err)
		})
	}
	o = base()
	_, err = ImageValuation(o, catalogs.ProviderOperationImagesEdits, time.Now())
	require.Error(t, err)
	for _, size := range []string{"auto", "1024", "-1x1024", "0x1024", "9223372036854775807x2", "1024x1024x2"} {
		_, err := ImageQuantities(base().Billing.Images, 1, size)
		require.Error(t, err, size)
	}
	_, err = ImageQuantities(base().Billing.Images, -1, "")
	require.Error(t, err)
	_, err = ImageQuantities(base().Billing.Images, math.MaxInt64, "")
	require.ErrorIs(t, err, reservation.ErrOverflow)
	o = base()
	o.Billing.Images.UnitPixels = math.MaxInt64
	o.Billing.Images.UnitIterations = 2
	_, err = ImageValuation(o, catalogs.ProviderOperationImagesGenerations, time.Now())
	require.ErrorIs(t, err, reservation.ErrOverflow)
	o = base()
	o.Pricing.Operations.ImageUnit = new(0.0)
	v, err = ImageValuation(o, catalogs.ProviderOperationImagesGenerations, time.Now())
	require.NoError(t, err)
	q, err = ImageQuantities(o.Billing.Images, 1, "")
	require.NoError(t, err)
	amount, err = v.NanoUSD(q)
	require.NoError(t, err)
	require.Zero(t, amount)
}

func TestImageValuationEditRequestCharge(t *testing.T) {
	offering := catalogs.ProviderOffering{
		Billing: &catalogs.ModelBilling{Images: &catalogs.ImageBilling{Basis: catalogs.ImageBillingImages, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationImagesEdits}, DefaultImages: 1, RequestCharge: new(true)}},
		Pricing: &catalogs.ModelPricing{Currency: "USD", Operations: &catalogs.ModelOperationPricing{ImageUnit: new(0.04), Request: new(0.01)}},
	}
	value, err := ImageValuation(offering, catalogs.ProviderOperationImagesEdits, time.Now())
	require.NoError(t, err)
	units, err := ImageQuantities(offering.Billing.Images, 2, "")
	require.NoError(t, err)
	amount, err := value.NanoUSD(units)
	require.NoError(t, err)
	require.EqualValues(t, 90000000, amount)
	_, err = ImageValuation(offering, catalogs.ProviderOperationImagesGenerations, time.Now())
	require.ErrorIs(t, err, reservation.ErrValuation)
}
