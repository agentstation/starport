package reservation

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackupTotalsPreserveAccountingBounds(t *testing.T) {
	for _, test := range []struct {
		name              string
		left, right, want BackupTotals
		invalid           bool
	}{
		{name: "exact-consumption-bound", left: BackupTotals{Consumed: math.MaxInt64 - 1}, right: BackupTotals{Consumed: 1}, want: BackupTotals{Consumed: math.MaxInt64}},
		{name: "saturated-consumption", left: BackupTotals{Consumed: math.MaxInt64}, right: BackupTotals{Consumed: 1}, want: BackupTotals{Consumed: math.MaxInt64, Overflow: true}},
		{name: "retained-valuation-overflow", left: BackupTotals{Reserved: 10, Overflow: true}, right: BackupTotals{Consumed: 3}, want: BackupTotals{Reserved: 10, Consumed: 3, Overflow: true}},
		{name: "reservation-overflow", left: BackupTotals{Reserved: math.MaxInt64}, right: BackupTotals{Reserved: 1}, invalid: true},
		{name: "dispute-overflow", left: BackupTotals{Disputes: math.MaxInt64}, right: BackupTotals{Disputes: 1}, invalid: true},
		{name: "negative-contribution", right: BackupTotals{Consumed: -1}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := AddBackupTotals(test.left, test.right)
			if test.invalid {
				require.ErrorIs(t, err, ErrUnavailable)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, actual)
		})
	}
}
