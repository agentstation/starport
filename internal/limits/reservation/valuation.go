// Package reservation owns durable budget capacity for individual provider attempts.
package reservation

import (
	"errors"
	"math/big"
	"regexp"
)

var (
	// ErrValuation reports an absent, invalid, or unsupported price contract.
	ErrValuation = errors.New("invalid reservation valuation")
	// ErrOverflow reports a charge outside the supported integer range.
	ErrOverflow = errors.New("reservation amount exceeds the integer range")
)

// ArithmeticVersion pins decimal arithmetic and one upward rounding per attempt.
const ArithmeticVersion = "decimal-nanousd-ceil-v1"

// Price is an exact USD amount for a declared number of billing units.
// An empty USD value is unknown. The string "0" is an explicit free price.
type Price struct {
	USD      string `json:"usd"`
	PerUnits int64  `json:"per_units"`
}

// Component names one disjoint billing component. Quantities must not overlap.
type Component struct {
	Unit  string `json:"unit"`
	Price Price  `json:"price"`
}

// Valuation pins all applicable rates for one selected offering and operation.
// The catalog projection supplies these rates before a reservation starts.
type Valuation struct {
	Version    string      `json:"version"`
	Components []Component `json:"components"`
}

// Quantities reports every component, including explicit zero quantities.
type Quantities map[string]int64

var decimalUSD = regexp.MustCompile(`^[0-9]{1,32}(\.[0-9]{1,32})?([eE][+-]?[0-9]{1,2})?$`)

func (v Valuation) validate() error {
	if v.Version != ArithmeticVersion || len(v.Components) == 0 || len(v.Components) > 16 {
		return ErrValuation
	}
	seen := make(map[string]bool, len(v.Components))
	for _, component := range v.Components {
		if !validID(component.Unit) || seen[component.Unit] || component.Price.PerUnits <= 0 || !decimalUSD.MatchString(component.Price.USD) {
			return ErrValuation
		}
		seen[component.Unit] = true
	}
	return nil
}

// NanoUSD sums exact decimal component charges, then rounds upward once.
func (v Valuation) NanoUSD(quantities Quantities) (int64, error) {
	if err := v.validate(); err != nil {
		return 0, err
	}
	if len(quantities) != len(v.Components) {
		return 0, ErrValuation
	}
	total := new(big.Rat)
	for _, component := range v.Components {
		quantity, found := quantities[component.Unit]
		if !found || quantity < 0 {
			return 0, ErrValuation
		}
		price, ok := new(big.Rat).SetString(component.Price.USD)
		if !ok || price.Sign() < 0 {
			return 0, ErrValuation
		}
		price.Mul(price, new(big.Rat).SetInt64(quantity))
		price.Quo(price, new(big.Rat).SetInt64(component.Price.PerUnits))
		total.Add(total, price)
	}
	total.Mul(total, new(big.Rat).SetInt64(1_000_000_000))
	integer, remainder := new(big.Int), new(big.Int)
	integer.QuoRem(total.Num(), total.Denom(), remainder)
	if remainder.Sign() != 0 {
		integer.Add(integer, big.NewInt(1))
	}
	if !integer.IsInt64() {
		return 0, ErrOverflow
	}
	return integer.Int64(), nil
}
