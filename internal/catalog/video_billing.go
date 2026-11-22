package catalog

import (
	"slices"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// VideoOutputSecondUnit identifies provider-reported output seconds.
const VideoOutputSecondUnit = "video_output_seconds"

// VideoValuation pins every declared charge for an output-duration video offering.
func VideoValuation(offering catalogs.ProviderOffering, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Videos == nil || billing.Validate() != nil || !validFlatUSDPricing(pricing, at) || pricing.Operations == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	operations := pricing.Operations
	if operations.InputSecond != nil || operations.VideoGen != nil || pricing.Tokens != nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	outComponent, ok := unitPriceComponent(VideoOutputSecondUnit, operations.OutputSecond)
	if !ok {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	result := reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{outComponent}}
	if *billing.Videos.RequestCharge {
		reqComponent, ok := unitPriceComponent(requestBillingUnit, operations.Request)
		if !ok {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		result.Components = append(result.Components, reqComponent)
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
