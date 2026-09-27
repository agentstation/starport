package admission

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type testPermission struct {
	policy    *limits.BudgetPolicy
	withdrawn atomic.Bool
}

var errWithdrawn = errors.New("permission withdrawn")

func (p *testPermission) Check() error {
	if p.withdrawn.Load() {
		return errWithdrawn
	}
	return nil
}

func (p *testPermission) BudgetPolicy() (*limits.BudgetPolicy, error) {
	return p.policy, p.Check()
}

func TestAbsentBudgetsStayLocal(t *testing.T) {
	policy, err := limits.NewBudgetPolicy(limits.BudgetHolder{ID: "account", Revision: 1}, limits.BudgetHolder{ID: "key", Revision: 1}, nil)
	require.NoError(t, err)
	permission := &testPermission{policy: policy}
	ctx := inference.WithPermission(t.Context(), permission)
	var owner *Owner
	quote := func(Requirements) (Quote, error) { panic("absent budgets must not request billing projection") }
	target := Target{AccountID: "account"}
	require.Zero(t, testing.AllocsPerRun(1000, func() {
		ticket, err := owner.Start(ctx, target, quote)
		if err != nil || ticket.ID() != "" {
			panic("absent policy failed")
		}
		if err := ticket.Finish(ctx, nil); err != nil {
			panic(err)
		}
	}))
	_, err = owner.Start(t.Context(), target, quote)
	require.ErrorIs(t, err, limits.ErrBudgetPolicyUnknown)
	_, err = owner.Start(ctx, Target{AccountID: "another"}, quote)
	require.ErrorIs(t, err, limits.ErrBudgetPolicyUnknown)
	permission.withdrawn.Store(true)
	_, err = owner.Start(ctx, target, quote)
	require.ErrorIs(t, err, errWithdrawn)
}

type admissionFixture struct {
	owner  *Owner
	ledger *reservation.Repository
	policy *testPermission
	ctx    context.Context
	meter  reservation.Meter
	target Target
}

func newAdmissionFixture(t *testing.T, tokens bool) admissionFixture {
	t.Helper()
	store, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	ledger, err := reservation.Open(store)
	require.NoError(t, err)
	owner, err := New(ledger, time.Second)
	require.NoError(t, err)
	holder := limits.BudgetHolder{ID: "account", Revision: 1}
	budget := &limits.Budget{Limit: 1000, Interval: limits.IntervalDay, HistoryID: "verified-history"}
	dimension := limits.DimensionSpend
	if tokens {
		holder.Tokens = budget
		dimension = limits.DimensionTokens
	} else {
		holder.Spend = budget
	}
	policy, err := limits.NewBudgetPolicy(holder, limits.BudgetHolder{ID: "key", Revision: 1}, nil)
	require.NoError(t, err)
	permission := &testPermission{policy: policy}
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "account", Dimension: dimension, Interval: limits.IntervalDay}
	require.NoError(t, ledger.EstablishWindow(t.Context(), meter, time.Now(), 0, reservation.History{ID: budget.HistoryID, Proof: "fixture-reconciliation"}))
	return admissionFixture{owner: owner, ledger: ledger, policy: permission, ctx: inference.WithPermission(t.Context(), permission), meter: meter, target: Target{AccountID: "account", RequestID: "request", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: "chat-completions"}}
}

func boundedQuote(units int64) QuoteFunc {
	return func(needed Requirements) (Quote, error) {
		quote := Quote{Tokens: new(units)}
		if needed.Spend {
			quote.Valuation = reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}
			quote.Units = reservation.Quantities{"output": units}
		}
		return quote, nil
	}
}

func TestAdmissionReservesExclusiveCapacity(t *testing.T) {
	for _, tokens := range []bool{false, true} {
		name := "spend"
		if tokens {
			name = "tokens"
		}
		t.Run(name, func(t *testing.T) {
			f := newAdmissionFixture(t, tokens)
			tickets := make(chan Ticket, 16)
			failures := make(chan error, 16)
			var wg sync.WaitGroup
			for range 16 {
				wg.Go(func() {
					ticket, err := f.owner.Start(f.ctx, f.target, boundedQuote(600))
					if err != nil {
						failures <- err
					} else {
						tickets <- ticket
					}
				})
			}
			wg.Wait()
			close(tickets)
			close(failures)
			require.Len(t, tickets, 1)
			require.Len(t, failures, 15)
			for err := range failures {
				require.ErrorIs(t, err, reservation.ErrExhausted)
			}
			ticket := <-tickets
			record, err := f.ledger.Inspect(t.Context(), ticket.ID())
			require.NoError(t, err)
			require.Equal(t, reservation.Dispatched, record.State)
			if tokens {
				require.Nil(t, record.NanoUSD)
			} else {
				require.EqualValues(t, 600, *record.NanoUSD)
			}
			evidence := &reservation.Evidence{ID: "provider-result", Tokens: 500}
			if !tokens {
				evidence.Quantities = reservation.Quantities{"output": 500}
			}
			// Required accounting survives caller cancellation and accepts exact retry.
			canceled, cancel := context.WithCancel(f.ctx)
			cancel()
			require.NoError(t, ticket.Finish(canceled, evidence))
			require.NoError(t, ticket.Finish(canceled, evidence))
			state, err := f.ledger.Window(t.Context(), f.meter, time.Now())
			require.NoError(t, err)
			require.EqualValues(t, 500, state.Consumed)
			require.Zero(t, state.Reserved)
			_, err = f.owner.Start(f.ctx, f.target, boundedQuote(600))
			require.ErrorIs(t, err, reservation.ErrExhausted)
			next, err := f.owner.Start(f.ctx, f.target, boundedQuote(400))
			require.NoError(t, err)
			require.NotEqual(t, ticket.ID(), next.ID(), "a retry needs separate charge identity")
			require.NoError(t, next.Finish(f.ctx, nil))
			state, err = f.ledger.Window(t.Context(), f.meter, time.Now())
			require.NoError(t, err)
			require.EqualValues(t, 400, state.Reserved, "missing usage cannot refund capacity")
		})
	}
}

type interruptedLedger struct {
	Ledger
	afterReserve             func()
	beginAcknowledgementLost bool
	failSettlement           atomic.Bool
}

func (l *interruptedLedger) Reserve(ctx context.Context, attempt reservation.Attempt) (*reservation.Record, error) {
	record, err := l.Ledger.Reserve(ctx, attempt)
	if err == nil && l.afterReserve != nil {
		l.afterReserve()
	}
	return record, err
}

func (l *interruptedLedger) Begin(ctx context.Context, id string) error {
	if err := l.Ledger.Begin(ctx, id); err != nil {
		return err
	}
	if l.beginAcknowledgementLost {
		return errors.New("injected begin acknowledgement loss")
	}
	return nil
}

func (l *interruptedLedger) Reconcile(ctx context.Context, id string, evidence reservation.Evidence) error {
	if l.failSettlement.Swap(false) {
		return errors.New("injected settlement failure")
	}
	return l.Ledger.Reconcile(ctx, id, evidence)
}

func TestAdmissionFailureBoundaries(t *testing.T) {
	for _, mode := range []string{"withdrawal", "lost-begin-ack", "settlement-failure", "unknown-bound", "unknown-price"} {
		t.Run(mode, func(t *testing.T) {
			f := newAdmissionFixture(t, mode == "unknown-bound")
			fault := &interruptedLedger{Ledger: f.ledger}
			owner, err := New(fault, time.Second)
			require.NoError(t, err)
			quote := boundedQuote(600)
			switch mode {
			case "withdrawal":
				fault.afterReserve = func() { f.policy.withdrawn.Store(true) }
			case "lost-begin-ack":
				fault.beginAcknowledgementLost = true
			case "settlement-failure":
				fault.failSettlement.Store(true)
			case "unknown-bound", "unknown-price":
				quote = func(Requirements) (Quote, error) { return Quote{}, nil }
			}
			ticket, err := owner.Start(f.ctx, f.target, quote)
			switch mode {
			case "withdrawal":
				require.ErrorIs(t, err, errWithdrawn)
			case "lost-begin-ack":
				require.ErrorContains(t, err, "acknowledgement loss")
				require.Empty(t, ticket.ID(), "failed admission returns no dispatch ticket")
				var detail *Error
				require.ErrorAs(t, err, &detail)
				record, err := f.ledger.Inspect(t.Context(), detail.AttemptID)
				require.NoError(t, err)
				require.Equal(t, reservation.Dispatched, record.State)
			case "unknown-bound":
				require.ErrorIs(t, err, ErrBoundUnknown)
			case "unknown-price":
				require.ErrorIs(t, err, reservation.ErrValuation)
			case "settlement-failure":
				require.NoError(t, err)
				evidence := &reservation.Evidence{ID: "measured", Quantities: reservation.Quantities{"output": 400}, Tokens: 400}
				require.ErrorContains(t, ticket.Finish(f.ctx, evidence), "settlement failure")
				record, err := f.ledger.Inspect(t.Context(), ticket.ID())
				require.NoError(t, err)
				require.Equal(t, reservation.Uncertain, record.State)
				require.Equal(t, evidence, record.Pending)
				state, err := f.ledger.Window(t.Context(), f.meter, time.Now())
				require.NoError(t, err)
				require.EqualValues(t, 600, state.Reserved)
				require.NoError(t, f.ledger.ReconcileRetained(t.Context(), ticket.ID()))
				require.NoError(t, ticket.Finish(f.ctx, evidence))
			}
			state, err := f.ledger.Window(t.Context(), f.meter, time.Now())
			require.NoError(t, err)
			if mode == "lost-begin-ack" {
				require.EqualValues(t, 600, state.Reserved)
			} else {
				require.Zero(t, state.Reserved)
			}
		})
	}
}
