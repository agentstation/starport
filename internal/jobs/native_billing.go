package jobs

import (
	"encoding/hex"
	"maps"
	"slices"

	"github.com/agentstation/starport/internal/limits/reservation"
)

const assetRecoveryExpired = "expired"

func copyValuation(v *reservation.Valuation) *reservation.Valuation {
	if v == nil {
		return nil
	}
	result := *v
	result.Components = slices.Clone(v.Components)
	return &result
}

func copyMeasurement(e *reservation.Evidence) *reservation.Evidence {
	if e == nil {
		return nil
	}
	result := *e
	result.Quantities = maps.Clone(e.Quantities)
	return &result
}

func (j Job) validateNative() error {
	if j.nativeAssetDigest != "" {
		digest, err := hex.DecodeString(j.nativeAssetDigest)
		if err != nil || len(digest) != 32 || j.nativeAssetContentType == "" || !j.Native || j.State != JobStateCompleted {
			return ErrInvalidJob
		}
	} else if j.nativeAssetContentType != "" {
		return ErrInvalidJob
	}
	switch j.assetRecoveryStatus {
	case "":
	case "blocked", "retry", "invalid", assetRecoveryExpired:
		if !j.Native || j.SubmissionPending || j.State != JobStateCompleted {
			return ErrInvalidJob
		}
	default:
		return ErrInvalidJob
	}
	if j.Native {
		if j.nativeReceiptKey == "" || j.nativeAssetKey == "" || j.CatalogGeneration == "" || j.nativeAssetBound <= 0 || j.nativeRetention <= 0 {
			return ErrInvalidJob
		}
		if !j.SubmissionPending && !j.State.Terminal() {
			return ErrInvalidJob
		}
	} else if j.nativeReceiptKey != "" || j.nativeAssetKey != "" || j.nativeAssetBound != 0 || j.nativeRetention != 0 {
		return ErrInvalidJob
	}
	if j.Valuation != nil {
		zero := reservation.Quantities{}
		for _, c := range j.Valuation.Components {
			zero[c.Unit] = 0
		}
		if _, err := j.Valuation.NanoUSD(zero); err != nil {
			return ErrInvalidJob
		}
	}
	return j.validateNativeMeasurement()
}

func (j Job) validateNativeMeasurement() error {
	if j.Measurement != nil {
		id := j.ID
		if j.ReservationID != "" {
			id = j.ReservationID
		}
		if j.Measurement.NoCharge || j.Measurement.ID != id+":usage" || j.Measurement.Tokens < 0 || len(j.Measurement.Quantities) == 0 || len(j.Measurement.Quantities) > 16 {
			return ErrInvalidJob
		}
		for unit, quantity := range j.Measurement.Quantities {
			if unit == "" || quantity < 0 {
				return ErrInvalidJob
			}
		}
		if j.Valuation != nil {
			if _, err := j.Valuation.NanoUSD(j.Measurement.Quantities); err != nil {
				return ErrInvalidJob
			}
		}
	}
	return nil
}
