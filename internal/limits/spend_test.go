package limits

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// An allowance answers a question the budget gate cannot: not whether the
// window is already spent, but whether one known price still fits inside it.
// Work priced before it runs is refused before it runs.

// TestWorkInsideTheAllowanceIsPaidFor states the ordinary case, and the exact
// boundary with it. Work priced at the whole remaining allowance is work the
// holder can pay for, so it runs.
func TestWorkInsideTheAllowanceIsPaidFor(t *testing.T) {
	t.Parallel()
	allowance := Allowance{NanoUSD: 500_000, Bounded: true}
	require.NoError(t, allowance.Covers(1))
	require.NoError(t, allowance.Covers(499_999))
	require.NoError(t, allowance.Covers(500_000),
		"a holder was refused work its remaining budget paid for exactly")
}

// TestWorkPastTheAllowanceIsRefused is the acceptance case. The refusal is
// typed, because the caller that raises it is deep inside a request and the
// answer the caller receives has to say the account ran out of budget rather
// than that a provider failed.
func TestWorkPastTheAllowanceIsRefused(t *testing.T) {
	t.Parallel()
	allowance := Allowance{NanoUSD: 500_000, Bounded: true}
	require.ErrorIs(t, allowance.Covers(500_001), ErrSpendLimitExceeded)
	require.ErrorIs(t, Allowance{Bounded: true}.Covers(1), ErrSpendLimitExceeded,
		"a holder with nothing left paid for more work")
}

// TestAHolderWithNoBudgetIsRefusedNothing holds the distinction the Bounded
// field exists for. Most deployments set no spend budget at all, and a holder
// without one reads as zero remaining. Treating that zero as an exhausted
// budget would refuse every priced request in an unmetered deployment.
func TestAHolderWithNoBudgetIsRefusedNothing(t *testing.T) {
	t.Parallel()
	require.NoError(t, Allowance{}.Covers(1_000_000_000_000))
}

// TestTheAllowanceTravelsWithTheRequest states the carrier. The gate reads the
// meter once at the door, and the step that prices the work is far from it.
func TestTheAllowanceTravelsWithTheRequest(t *testing.T) {
	t.Parallel()
	ctx := ContextWithAllowance(context.Background(), Allowance{NanoUSD: 42, Bounded: true})
	carried := AllowanceFromContext(ctx)
	require.True(t, carried.Bounded)
	require.EqualValues(t, 42, carried.NanoUSD)
}

// TestARequestThatPassedNoGateIsUnbounded states how the carrier fails.
//
// A request reaching a priced step without an allowance found no budget, which
// is not the same as finding an empty one. Reading it as empty would make every
// path that skips the gate refuse its work, and the gateway would break exactly
// where it is least metered.
func TestARequestThatPassedNoGateIsUnbounded(t *testing.T) {
	t.Parallel()
	require.False(t, AllowanceFromContext(context.Background()).Bounded)
	require.NoError(t, AllowanceFromContext(context.Background()).Covers(1_000_000))
}
