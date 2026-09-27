package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestVideoValuationUsesPinnedMeasuredSeconds(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	offering, err := client.Catalog().Offering("deepinfra", "Wan-AI/Wan2.2-T2V-A14B")
	require.NoError(t, err)
	require.NotNil(t, offering.Billing)
	valuation, err := VideoValuation(offering, time.Now())
	require.NoError(t, err)
	bound, err := VideoRequestQuantities(offering.Billing.Videos, 0, "")
	require.NoError(t, err)
	amount, err := valuation.NanoUSD(bound)
	require.NoError(t, err)
	require.Equal(t, int64(375000000), amount)
	// A later catalog mutation cannot change the submitted valuation.
	*offering.Pricing.Operations.OutputSecond = 99
	measured, err := VideoMeasuredQuantities(offering.Billing.Videos, 5)
	require.NoError(t, err)
	amount, err = valuation.NanoUSD(measured)
	require.NoError(t, err)
	require.Equal(t, int64(375000000), amount)
	zero, err := VideoMeasuredQuantities(offering.Billing.Videos, 0)
	require.NoError(t, err)
	amount, err = valuation.NanoUSD(zero)
	require.NoError(t, err)
	require.Zero(t, amount)
	for _, seconds := range []int64{-1, 2, 6} {
		_, err := VideoRequestQuantities(offering.Billing.Videos, seconds, "")
		require.ErrorIs(t, err, reservation.ErrValuation)
	}
	_, err = VideoRequestQuantities(offering.Billing.Videos, 5, "640x480")
	require.ErrorIs(t, err, reservation.ErrValuation)
	// Measured overage must remain evidence for reconciliation, not disappear.
	over, err := VideoMeasuredQuantities(offering.Billing.Videos, 6)
	require.NoError(t, err)
	amount, err = valuation.NanoUSD(over)
	require.NoError(t, err)
	require.Equal(t, int64(450000000), amount)
}

func TestVideoValuationRefusesUnknownAdditionalCharges(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	source, err := client.Catalog().Offering("deepinfra", "Wan-AI/Wan2.2-T2V-A14B")
	require.NoError(t, err)
	for _, change := range []func(*catalogs.ProviderOffering){
		func(o *catalogs.ProviderOffering) { o.Billing = nil },
		func(o *catalogs.ProviderOffering) { o.Pricing.Operations.OutputSecond = nil },
		func(o *catalogs.ProviderOffering) { rate := 0.5; o.Pricing.Operations.InputSecond = &rate },
		func(o *catalogs.ProviderOffering) { rate := 0.5; o.Pricing.Operations.VideoGen = &rate },
		func(o *catalogs.ProviderOffering) { rate := 0.5; o.Pricing.Operations.Request = &rate },
		func(o *catalogs.ProviderOffering) { o.Pricing.Currency = "EUR" },
	} {
		copy, err := client.Catalog().Offering("deepinfra", "Wan-AI/Wan2.2-T2V-A14B")
		require.NoError(t, err)
		change(&copy)
		_, err = VideoValuation(copy, time.Now())
		require.ErrorIs(t, err, reservation.ErrValuation)
	}
	billing := source.Billing.Videos
	charge := true
	billing.RequestCharge = &charge
	_, err = VideoValuation(source, time.Now())
	require.ErrorIs(t, err, reservation.ErrValuation)
	rate := 0.01
	source.Pricing.Operations.Request = &rate
	valuation, err := VideoValuation(source, time.Now())
	require.NoError(t, err)
	measured, err := VideoMeasuredQuantities(billing, 0)
	require.NoError(t, err)
	amount, err := valuation.NanoUSD(measured)
	require.NoError(t, err)
	require.Equal(t, int64(10000000), amount)
}
