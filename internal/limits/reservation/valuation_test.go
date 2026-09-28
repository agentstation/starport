package reservation

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValuation(t *testing.T) {
	v := Valuation{Version: ArithmeticVersion, Components: []Component{
		{Unit: "input", Price: Price{USD: "0.0000000004", PerUnits: 1}},
		{Unit: "output", Price: Price{USD: "0.0000000004", PerUnits: 1}},
	}}
	amount, err := v.NanoUSD(Quantities{"input": 1, "output": 1})
	require.NoError(t, err)
	require.EqualValues(t, 1, amount, "round once after summing components")
	v.Components[0].Price = Price{USD: "0.1", PerUnits: 1_000_000}
	v.Components[1].Price = Price{USD: "3e-7", PerUnits: 1}
	amount, err = v.NanoUSD(Quantities{"input": 3, "output": 2})
	require.NoError(t, err)
	require.EqualValues(t, 900, amount, "decimal rates must not acquire a binary rounding penny")
	for _, quantities := range []Quantities{{"input": 1}, {"input": -1, "output": 0}, {"input": 1, "output": 1, "extra": 1}} {
		_, err := v.NanoUSD(quantities)
		require.ErrorIs(t, err, ErrValuation)
	}
	for _, price := range []string{"", "-1", "NaN", "+Inf", "1/2", "0x1p0", "1e999999"} {
		v.Components[0].Price.USD = price
		_, err := v.NanoUSD(Quantities{"input": 1, "output": 0})
		require.ErrorIs(t, err, ErrValuation, price)
	}
	v.Components[0].Price = Price{USD: "0", PerUnits: 1}
	amount, err = v.NanoUSD(Quantities{"input": math.MaxInt64, "output": 0})
	require.NoError(t, err)
	require.Zero(t, amount)
	v.Components[0].Price.USD = "1"
	_, err = v.NanoUSD(Quantities{"input": math.MaxInt64, "output": 0})
	require.ErrorIs(t, err, ErrOverflow)
}
