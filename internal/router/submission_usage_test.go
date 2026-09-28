package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type measuredSubmissionPermission struct{ policy *limits.BudgetPolicy }

func (p measuredSubmissionPermission) Check() error { return nil }
func (p measuredSubmissionPermission) BudgetPolicy() (*limits.BudgetPolicy, error) {
	return p.policy, nil
}

func TestSubmissionWriteFailureRetainsMeasuredUsage(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "measured"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			store, err := storage.OpenBadger(storage.BadgerConfig{Path: t.TempDir(), SyncWrites: true, NumVersions: 1, MemTableSize: 64 << 20})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			ledger, err := reservation.Open(store)
			require.NoError(t, err)
			owner, err := admission.New(ledger, time.Second)
			require.NoError(t, err)
			policy, err := limits.NewBudgetPolicy(limits.BudgetHolder{ID: "account", Revision: 1, Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay, HistoryID: "history"}}, limits.BudgetHolder{ID: "key", Revision: 1}, nil)
			require.NoError(t, err)
			ctx := inference.WithPermission(t.Context(), measuredSubmissionPermission{policy})
			meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "account", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
			require.NoError(t, ledger.EstablishWindow(ctx, meter, time.Now(), 0, reservation.History{ID: "history", Proof: "fixture"}))
			calls := 0
			commitFailure := errors.New("acceptance write failed")
			operation := providerCall[*connectors.NativeVideoRequest, *connectors.NativeVideoResponse, connectors.NativeVideoResponse]{
				transport: func(connectors.Connector, catalogs.EndpointType) (providerInvoke[*connectors.NativeVideoRequest, *connectors.NativeVideoResponse], bool) {
					return func(context.Context, *connectors.NativeVideoRequest) (*connectors.NativeVideoResponse, error) {
						calls++
						if missing {
							return nil, errors.New("response lost")
						}
						seconds := int64(5)
						return &connectors.NativeVideoResponse{OutputSeconds: &seconds}, nil
					}, true
				},
				build: func() *connectors.NativeVideoRequest { return &connectors.NativeVideoRequest{} },
				convert: func(r *connectors.NativeVideoResponse) (connectors.NativeVideoResponse, error) {
					t.Fatal("failed persistence reached conversion")
					return *r, nil
				},
				afterDispatch: func(context.Context, routing.Route, *connectors.NativeVideoResponse, error) error {
					return commitFailure
				},
				charge: func(*runtimecatalog.RoutableSnapshot, routing.Route, *connectors.NativeVideoRequest) operationCharge[*connectors.NativeVideoResponse] {
					return operationCharge[*connectors.NativeVideoResponse]{
						quote: func(admission.Requirements) (admission.Quote, error) {
							return admission.Quote{Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "seconds", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Units: reservation.Quantities{"seconds": 10}}, nil
						},
						evidence: func(r *connectors.NativeVideoResponse, _ error) *reservation.Evidence {
							if r == nil || r.OutputSeconds == nil {
								return nil
							}
							return &reservation.Evidence{Quantities: reservation.Quantities{"seconds": *r.OutputSeconds}}
						},
					}
				},
			}
			budget := operationBudget{start: func(quote admission.QuoteFunc) (admission.Ticket, *failure.Failure) {
				ticket, err := owner.Start(ctx, admission.Target{AccountID: "account", RequestID: "request", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: "videos-generations"}, quote)
				require.NoError(t, err)
				return ticket, nil
			}}
			_, refusal, action := operation.attempt(routing.OperationVideosGenerations)(ctx, nil, routing.Route{}, credentialSelection{}, budget)
			require.NotNil(t, refusal)
			require.ErrorIs(t, refusal, commitFailure)
			require.Equal(t, execution.AttemptActionStop, action)
			require.Equal(t, 1, calls)
			window, err := ledger.Window(ctx, meter, time.Now())
			require.NoError(t, err)
			if missing {
				require.EqualValues(t, 10, window.Reserved)
				require.Zero(t, window.Consumed)
			} else {
				require.Zero(t, window.Reserved, "durable acceptance failure must not discard measured usage")
				require.EqualValues(t, 5, window.Consumed)
			}
		})
	}
}
