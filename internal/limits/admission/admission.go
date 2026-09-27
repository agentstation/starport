// Package admission owns permission, capacity, and settlement for paid attempts.
package admission

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
)

// ErrBoundUnknown refuses work without the required enforceable upper bound.
var ErrBoundUnknown = errors.New("required provider billing bound is unknown")

// Ledger is the durable reservation contract. Its store must have independent
// recovery approval. A nil ledger cannot authorize a required budget.
type Ledger interface {
	Reserve(context.Context, reservation.Attempt) (*reservation.Record, error)
	Begin(context.Context, string) error
	CancelBeforeDispatch(context.Context, string) error
	MarkUncertain(context.Context, string, string) error
	Reconcile(context.Context, string, reservation.Evidence) error
	RetainEvidence(context.Context, string, reservation.Evidence) error
}

// Owner coordinates one paid attempt. It starts no background work.
type Owner struct {
	ledger            Ledger
	settlementTimeout time.Duration
}

// New creates an admission owner. The timeout bounds required accounting after
// a caller disconnects. Storage failure retains reserved capacity.
func New(ledger Ledger, settlementTimeout time.Duration) (*Owner, error) {
	if ledger == nil || settlementTimeout <= 0 {
		return nil, reservation.ErrUnavailable
	}
	return &Owner{ledger: ledger, settlementTimeout: settlementTimeout}, nil
}

// Target identifies the selected offering and one operation within a request.
// Operation distinguishes independently charged follow-up calls from submission.
type Target struct {
	RequestID         string
	AccountID         string
	OfferingID        string
	CatalogGeneration string
	Operation         string
}

// Requirements identifies the dimensions that the admitted policy limits.
type Requirements struct{ Spend, Tokens bool }

// Quote contains bounds enforced by the prepared request and catalog contract.
// Tokens is nil when token use is unknown. Valuation and Units are required for
// spend meters. Token-only admission retains unknown money as null.
type Quote struct {
	Valuation reservation.Valuation
	Units     reservation.Quantities
	Tokens    *int64
}

// QuoteFunc projects the selected catalog offering and prepared request. It runs
// only when a required budget exists. It must not perform provider acquisition.
type QuoteFunc func(Requirements) (Quote, error)

// Error identifies a reservation whose capacity may need reconciliation.
type Error struct {
	AttemptID string
	Cause     error
}

func (e *Error) Error() string { return fmt.Sprintf("budget attempt %s: %v", e.AttemptID, e.Cause) }
func (e *Error) Unwrap() error { return e.Cause }

// Ticket proves successful consumption of a single dispatch permit. The zero
// value represents confirmed absence of budgets and performs no accounting.
type Ticket struct {
	owner     *Owner
	id        string
	tokenOnly bool
}

// ID names the reservation, or returns empty for confirmed absent budgets.
func (t Ticket) ID() string { return t.id }

// Start must run immediately before each actual provider call, including retries
// and paid child operations. Missing policy never means unlimited permission.
func (o *Owner) Start(ctx context.Context, target Target, quote QuoteFunc) (Ticket, error) {
	permission := inference.RequestPermission(ctx)
	reader, ok := permission.(limits.BudgetPolicyReader)
	if !ok {
		return Ticket{}, limits.ErrBudgetPolicyUnknown
	}
	policy, err := reader.BudgetPolicy()
	if err != nil {
		return Ticket{}, err
	}
	if policy == nil {
		return Ticket{}, limits.ErrBudgetPolicyUnknown
	}
	account, _, _ := policy.Identity()
	if target.AccountID != account {
		return Ticket{}, limits.ErrBudgetPolicyUnknown
	}
	if err := ctx.Err(); err != nil {
		return Ticket{}, err
	}
	if policy.RuleCount() == 0 {
		return Ticket{}, nil
	}
	if o == nil || o.ledger == nil {
		return Ticket{}, reservation.ErrUnavailable
	}
	attempt, err := prepareAttempt(policy, target, quote)
	if err != nil {
		return Ticket{}, err
	}
	if _, err := o.ledger.Reserve(ctx, attempt); err != nil {
		return Ticket{}, &Error{AttemptID: attempt.ID, Cause: err}
	}
	// A withdrawal during a storage operation cannot authorize dispatch.
	if err := errors.Join(ctx.Err(), permission.Check()); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.settlementTimeout)
		defer cancel()
		return Ticket{}, &Error{AttemptID: attempt.ID, Cause: errors.Join(err, o.ledger.CancelBeforeDispatch(cleanup, attempt.ID))}
	}
	if err := o.ledger.Begin(ctx, attempt.ID); err != nil {
		// An ambiguous acknowledgement may have consumed the permit. Keep its
		// capacity and never dispatch or issue a refund based on that error.
		return Ticket{}, &Error{AttemptID: attempt.ID, Cause: err}
	}
	if err := errors.Join(ctx.Err(), permission.Check()); err != nil {
		return Ticket{}, &Error{AttemptID: attempt.ID, Cause: err}
	}
	return Ticket{owner: o, id: attempt.ID, tokenOnly: attempt.TokenOnly}, nil
}

func prepareAttempt(policy *limits.BudgetPolicy, target Target, quote QuoteFunc) (reservation.Attempt, error) {
	if quote == nil {
		return reservation.Attempt{}, ErrBoundUnknown
	}
	account, key, team := policy.Identity()
	attempt := reservation.Attempt{RequestID: target.RequestID, AccountID: account, KeyID: key, TeamID: team, OfferingID: target.OfferingID, CatalogGeneration: target.CatalogGeneration, Operation: target.Operation}
	needed := Requirements{}
	for index := range policy.RuleCount() {
		rule, _ := policy.Rule(index)
		needed.Spend = needed.Spend || rule.Dimension == limits.DimensionSpend
		needed.Tokens = needed.Tokens || rule.Dimension == limits.DimensionTokens
		attempt.Rules = append(attempt.Rules, reservation.Rule{
			Meter: reservation.Meter{Scope: rule.Scope, Holder: rule.Holder, Dimension: rule.Dimension, Interval: rule.Budget.Interval},
			Limit: rule.Budget.Limit, PolicyRevision: strconv.FormatUint(rule.Revision, 10), HistoryID: rule.Budget.HistoryID,
		})
	}
	bound, err := quote(needed)
	if err != nil {
		return reservation.Attempt{}, err
	}
	if needed.Tokens && bound.Tokens == nil || bound.Tokens != nil && *bound.Tokens < 0 {
		return reservation.Attempt{}, ErrBoundUnknown
	}
	attempt.ID = rand.Text()
	attempt.TokenOnly = !needed.Spend
	if needed.Spend {
		attempt.Valuation, attempt.Bound = bound.Valuation, bound.Units
	}
	if bound.Tokens != nil {
		attempt.TokenBound = *bound.Tokens
	}
	return attempt, nil
}
