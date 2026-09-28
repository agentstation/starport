package catalog

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits/reservation"
)

const (
	imageCountUnit           = "images"
	imagePixelIterationsUnit = "image_pixel_iterations"
)

// ImageValuation projects complete catalog image charges for one operation.
func ImageValuation(offering catalogs.ProviderOffering, operation catalogs.ProviderOperation, at time.Time) (reservation.Valuation, error) {
	billing, pricing := offering.Billing, offering.Pricing
	if billing == nil || billing.Images == nil || billing.Validate() != nil || !slices.Contains(billing.Images.Operations, operation) || pricing == nil || pricing.Validate() != nil || pricing.Currency != catalogs.ModelPricingCurrencyUSD || !pricing.IsEffectiveAt(at) || len(pricing.Tiers) != 0 || pricing.Operations == nil || pricing.Operations.ImageUnit == nil {
		return reservation.Valuation{}, reservation.ErrValuation
	}
	unit, per := imageCountUnit, int64(1)
	if billing.Images.Basis == catalogs.ImageBillingPixelIterations {
		unit = imagePixelIterationsUnit
		var err error
		per, err = imageProduct(billing.Images.UnitPixels, billing.Images.UnitIterations)
		if err != nil {
			return reservation.Valuation{}, err
		}
	}
	v := reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: unit, Price: reservation.Price{USD: strconv.FormatFloat(*pricing.Operations.ImageUnit, 'g', -1, 64), PerUnits: per}}}}
	if *billing.Images.RequestCharge {
		if pricing.Operations.Request == nil {
			return reservation.Valuation{}, reservation.ErrValuation
		}
		v.Components = append(v.Components, reservation.Component{Unit: requestBillingUnit, Price: reservation.Price{USD: strconv.FormatFloat(*pricing.Operations.Request, 'g', -1, 64), PerUnits: 1}})
	}
	return v, nil
}

// ImageQuantities counts submitted or completed images under one catalog contract.
// A zero count selects the declared request default. Settlement must supply a positive count.
func ImageQuantities(billing *catalogs.ImageBilling, count int64, size string) (reservation.Quantities, error) {
	if billing == nil || (&catalogs.ModelBilling{Images: billing}).Validate() != nil || count < 0 {
		return nil, reservation.ErrValuation
	}
	if count == 0 {
		count = billing.DefaultImages
	}
	unit, quantity := imageCountUnit, count
	if billing.Basis == catalogs.ImageBillingPixelIterations {
		width, height := billing.DefaultWidth, billing.DefaultHeight
		if size != "" {
			w, h, found := strings.Cut(size, "x")
			if !found {
				return nil, reservation.ErrValuation
			}
			var err error
			width, err = strconv.ParseInt(w, 10, 64)
			if err != nil {
				return nil, reservation.ErrValuation
			}
			height, err = strconv.ParseInt(h, 10, 64)
			if err != nil {
				return nil, reservation.ErrValuation
			}
		}
		var err error
		quantity, err = imageProduct(count, width, height, billing.Iterations)
		if err != nil {
			return nil, err
		}
		unit = imagePixelIterationsUnit
	}
	result := reservation.Quantities{unit: quantity}
	if *billing.RequestCharge {
		result[requestBillingUnit] = 1
	}
	return result, nil
}

func imageProduct(values ...int64) (int64, error) {
	product := int64(1)
	for _, v := range values {
		if v <= 0 {
			return 0, reservation.ErrValuation
		}
		if product > math.MaxInt64/v {
			return 0, reservation.ErrOverflow
		}
		product *= v
	}
	return product, nil
}
