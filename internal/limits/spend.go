package limits

import (
	"context"
	"errors"
)

// A budget refuses a request at the door, after the meter says the window is
// already spent. That answers one question and not the other. Some work inside
// a request costs money before the provider call the request came for: a
// document sent to a recognition model is paid for by the page, and it is paid
// for whether or not the chat model that reads the text ever runs.
//
// An allowance is what the door already worked out: how much of the tightest
// spend budget is left in the current window. The gate computes it once and
// carries it, so a step deeper in the request refuses expensive work with the
// same number the caller sees in its headers, and no second read of the meter.

// ErrSpendLimitExceeded reports work a holder's spend budget cannot pay for.
var ErrSpendLimitExceeded = errors.New("spend limit exceeded")

// Allowance is what one holder may still spend in its current budget window,
// in integer nano-USD.
//
// An unbounded allowance is the normal case: most deployments set no spend
// budget at all, and a holder without one is not metered against anything.
// Bounded says which of the two a zero means, because a holder with nothing
// left and a holder with no budget are opposite answers.
type Allowance struct {
	// NanoUSD is what remains in the window. It is meaningful only when
	// Bounded is true.
	NanoUSD int64
	// Bounded reports that a spend budget applies to this holder.
	Bounded bool
}

// Covers reports whether the allowance pays for work priced at nanoUSD.
//
// It refuses the work that crosses the bound rather than the work after it. A
// caller at the door is refused on a window already spent, which lets the
// crossing request through; here the price is known before the money is spent,
// so refusing first is both possible and cheaper for the account.
func (a Allowance) Covers(nanoUSD int64) error {
	if !a.Bounded || nanoUSD <= a.NanoUSD {
		return nil
	}
	return ErrSpendLimitExceeded
}

// allowanceKey is the private context key the allowance travels under.
type allowanceKey struct{}

// ContextWithAllowance carries one holder's remaining spend into the request.
func ContextWithAllowance(ctx context.Context, allowance Allowance) context.Context {
	return context.WithValue(ctx, allowanceKey{}, allowance)
}

// AllowanceFromContext reads the remaining spend the budget gate recorded.
//
// A request that never passed the gate reads as unbounded. That is the honest
// answer rather than a permissive default: no budget was found, so no budget
// refuses anything.
func AllowanceFromContext(ctx context.Context) Allowance {
	allowance, ok := ctx.Value(allowanceKey{}).(Allowance)
	if !ok {
		return Allowance{}
	}
	return allowance
}
