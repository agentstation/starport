package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

// reportingFailureStore keeps real Badger operations except optional usage commits.
// Its authority interface delegates every required budget operation unchanged.
type reportingFailureStore struct {
	storage.KVStore
	storage.TimeBoundStore
	failed atomic.Int64
}

func (s *reportingFailureStore) CompareAndSwapBatch(ctx context.Context, mutations []storage.CompareAndSwapMutation) error {
	for _, mutation := range mutations {
		if strings.HasPrefix(mutation.Key, usage.StoragePrefix) {
			s.failed.Add(1)
			return errors.New("injected optional usage write failure")
		}
	}
	return s.KVStore.CompareAndSwapBatch(ctx, mutations)
}

func TestProductionBudgetSurvivesReportingFailure(t *testing.T) {
	for _, failure := range []string{"store", "export", "both"} {
		for _, measured := range []bool{true, false} {
			state := "uncertain"
			if measured {
				state = "measured"
			}
			t.Run(failure+"/"+state, func(t *testing.T) {
				var exportCalls atomic.Int64
				exporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					exportCalls.Add(1)
					http.Error(w, "injected reporting outage", http.StatusServiceUnavailable)
				}))
				t.Cleanup(exporter.Close)
				var failedStore *reportingFailureStore
				var exportDrops interface{ Dropped() int64 }
				inject := func(options *buildOptions) {
					if failure != "export" {
						open := options.factories.openStorage
						options.factories.openStorage = func(cfg storage.Config) (storage.KVStore, error) {
							store, err := open(cfg)
							if err != nil {
								return nil, err
							}
							authority, ok := store.(storage.TimeBoundStore)
							require.True(t, ok)
							failedStore = &reportingFailureStore{KVStore: store, TimeBoundStore: authority}
							return failedStore, nil
						}
					}
					build := options.factories.newServer
					options.factories.newServer = func(cfg *server.Config, dependencies server.Dependencies) (httpRuntime, error) {
						exportDrops = dependencies.Deployment.UsageExport
						return build(cfg, dependencies)
					}
				}
				fixture := newPerformanceFixtureWithRuntime(t, 0, nil, true,
					&limits.Limits{Tokens: &limits.Budget{Limit: 200_000, Interval: limits.IntervalDay}},
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						body := `{"id":"reporting-fixture","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]`
						if measured {
							body += `,"usage":{"prompt_tokens":100000,"completion_tokens":3,"total_tokens":100003}`
						}
						_, _ = io.WriteString(w, body+"}")
					}),
					[]performanceProvider{{catalogs.ProviderIDOpenAI, "OPENAI_API_KEY", "Authorization", "Bearer sk-test-key", []string{"/v1/chat/completions"}}},
					nil, []Option{inject}, func(cfg *config.Config) {
						if failure != "store" {
							cfg.Telemetry.UsageExport = exporter.URL
						}
					})
				require.Equal(t, http.StatusOK, budgetDispatch(t, fixture))
				keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
				require.NoError(t, err)
				require.Len(t, keys, 1)
				data, err := fixture.application.store.Get(t.Context(), keys[0])
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(data, &record))
				require.Len(t, record.Bindings, 1)
				binding := record.Bindings[0]
				before, err := fixture.application.budget.ledger.Window(t.Context(), binding.Rule.Meter, record.AdmittedAt)
				require.NoError(t, err)
				if measured {
					require.Equal(t, reservation.Settled, record.State)
					require.EqualValues(t, 100003, before.Consumed)
					require.Zero(t, before.Reserved)
				} else {
					require.Equal(t, reservation.Uncertain, record.State)
					require.Equal(t, binding.Amount, before.Reserved)
					require.Zero(t, before.Consumed)
				}
				if failedStore != nil {
					require.Eventually(t, func() bool { return failedStore.failed.Load() > 0 }, time.Second, time.Millisecond)
					reports, err := fixture.application.store.ScanWithPrefix(t.Context(), usage.StoragePrefix, 100)
					require.NoError(t, err)
					require.Empty(t, reports)
				}
				if failure != "store" {
					require.NotNil(t, exportDrops)
					require.Eventually(t, func() bool { return exportDrops.Dropped() > 0 }, 10*time.Second, 10*time.Millisecond)
					require.EqualValues(t, 3, exportCalls.Load(), "one export exhausts its bounded retries")
				}
				require.Equal(t, http.StatusPaymentRequired, budgetDispatch(t, fixture))
				require.EqualValues(t, 1, fixture.calls.Load(), "reporting failure must not restore capacity")
				after, err := fixture.application.budget.ledger.Window(t.Context(), binding.Rule.Meter, record.AdmittedAt)
				require.NoError(t, err)
				require.Equal(t, before, after)
				retained, err := fixture.application.store.Get(t.Context(), keys[0])
				require.NoError(t, err)
				require.Equal(t, data, retained)
			})
		}
	}
}
