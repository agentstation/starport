package catalog

import (
	"slices"
	"strconv"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// VideoOutputSecondUnit identifies provider-reported output seconds.
const VideoOutputSecondUnit = "video_output_seconds"

// VideoValuation pins every declared charge for an output-duration video offering.
func VideoValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Videos == nil || billing.Validate() != nil || pricing == nil || pricing.Validate() != nil || pricing.Currency != catalogs.ModelPricingCurrencyUSD || !pricing.IsEffectiveAt(at) || len(pricing.Tiers) != 0 || pricing.Operations == nil || pricing.Operations.OutputSecond == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	operations := pricing.Operations
	if operations.InputSecond != nil || operations.VideoGen != nil || pricing.Tokens != nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	result := reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: VideoOutputSecondUnit, Price: reservation.Price{USD: strconv.FormatFloat(*operations.OutputSecond, 'g', -1, 64), PerUnits: 1}}}}
	if *billing.Videos.RequestCharge {
		if operations.Request == nil {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, reservation.Component{Unit: requestBillingUnit, Price: reservation.Price{USD: strconv.FormatFloat(*operations.Request, 'g', -1, 64), PerUnits: 1}})
	} else if operations.Request != nil && *operations.Request != 0 {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	return result, nil
}

// VideoRequestQuantities resolves omitted inputs and bounds a declared video request.
// Its output is a reservation bound, never provider usage evidence.
func VideoRequestQuantities(billing *catalogs.VideoBilling, seconds int64, size string) (reservation.Quantities, error) {
	if billing == nil || (&catalogs.ModelBilling{Videos: billing}).Validate() != nil || seconds < 0 {
		return nil, reservation.ErrValuation
	}
	if seconds == 0 {
		seconds = billing.DefaultSeconds
	}
	if size == "" {
		size = billing.DefaultSize
	}
	if !slices.Contains(billing.Seconds, seconds) || !slices.Contains(billing.Sizes, size) {
		return nil, reservation.ErrValuation
	}
	return VideoMeasuredQuantities(billing, seconds)
}

// VideoMeasuredQuantities preserves measured duration, including an explicit zero.
// It never substitutes a requested duration or a catalog default.
// The caller must verify measured evidence for every declared charge.
func VideoMeasuredQuantities(billing *catalogs.VideoBilling, seconds int64) (reservation.Quantities, error) {
	if billing == nil || (&catalogs.ModelBilling{Videos: billing}).Validate() != nil || seconds < 0 {
		return nil, reservation.ErrValuation
	}
	quantities := reservation.Quantities{VideoOutputSecondUnit: seconds}
	if *billing.RequestCharge {
		quantities[requestBillingUnit] = 1
	}
	return quantities, nil
}
