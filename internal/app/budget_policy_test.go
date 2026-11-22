package app

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/server"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestProductionBudgetPreservesRequestIdentity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			var calls atomic.Int32
			fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true,
				&limits.Limits{Tokens: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}},
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if calls.Add(1) == 1 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":{"message":"retry fixture","type":"server_error"}}`)
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"id\":\"identity\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}\n\ndata: [DONE]\n\n")
					} else {
						budgetReply(w)
					}
				}))
			const requestID = "budget-request-identity"
			body := `{"model":"openai/gpt-4o-mini","models":["openai/gpt-4o-mini","openai/gpt-4o"],"stream":` + strconv.FormatBool(stream) + `,"max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`
			for range 2 {
				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/api/v1/chat/completions", strings.NewReader(body))
				require.NoError(t, err)
				request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Request-ID", requestID)
				response, err := fixture.client.Do(request)
				require.NoError(t, err)
				result, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusOK, response.StatusCode, string(result))
			}
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
			require.NoError(t, err)
			require.Len(t, keys, 3, "one failed attempt and two successful calls need separate reservations")
			require.EqualValues(t, 3, calls.Load())
			ids := map[string]bool{}
			states := map[reservation.State]int{}
			var uncertain reservation.Record
			for _, key := range keys {
				data, err := fixture.application.store.Get(t.Context(), key)
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(data, &record))
				require.Equal(t, requestID, record.Attempt.RequestID)
				require.NotEqual(t, requestID, record.Attempt.ID)
				require.False(t, ids[record.Attempt.ID], "a repeated request ID must not reuse a dispatch permit")
				ids[record.Attempt.ID] = true
				states[record.State]++
				if record.State == reservation.Uncertain {
					uncertain = record
				}
			}
			require.Equal(t, map[reservation.State]int{reservation.Uncertain: 1, reservation.Settled: 2}, states)
			for _, binding := range uncertain.Bindings {
				window, err := fixture.application.budget.ledger.Window(t.Context(), binding.Rule.Meter, uncertain.AdmittedAt)
				require.NoError(t, err)
				require.Equal(t, binding.Amount, window.Reserved)
				require.EqualValues(t, 22, window.Consumed, "both measured retries charge the same token meter")
			}
		})
	}
}

func TestRunRecoversRetainedBudgetEvidence(t *testing.T) {
	cfg := validProductionConfig(t)
	cfg.Jobs.SweepInterval = 24 * time.Hour
	factories := explicitTestFactories(t)
	factories.openCatalog = func(ctx context.Context, store storage.KVStore, _ *sqlstore.DB, _ runtimecatalog.Settings, _ runtimecatalog.DeploymentLookup) (catalogRuntime, error) {
		return newLifecycleCatalogRuntime(ctx, store)
	}
	fakeHTTP := newBlockingHTTPRuntime()
	factories.newServer = func(*server.Config, server.Dependencies) (httpRuntime, error) { return fakeHTTP, nil }
	application, err := New(cfg, withRuntimeFactories(factories))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close(context.Background())) })
	ledger := application.budget.ledger
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "recovery-account", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	now := time.Now()
	require.NoError(t, ledger.EstablishWindow(t.Context(), meter, now, 0, reservation.History{ID: "history", Proof: "empty-fixture-history"}))
	attempt := reservation.Attempt{
		ID: "recovery-attempt", RequestID: "recovery-request", AccountID: meter.Holder, KeyID: "recovery-key",
		OfferingID: "fixture/model", CatalogGeneration: "original-generation", Operation: "chat",
		Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "history"}},
		Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{
			{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}},
		}},
		Bound: reservation.Quantities{"output": 600},
	}
	_, err = ledger.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, ledger.Begin(t.Context(), attempt.ID))
	evidence := reservation.Evidence{ID: "provider-measured", Quantities: reservation.Quantities{"output": 200}}
	require.NoError(t, ledger.RetainEvidence(t.Context(), attempt.ID, evidence))
	record, err := ledger.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, reservation.Uncertain, record.State, "construction starts no recovery worker")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("application recovery worker did not stop")
		}
	})
	select {
	case <-fakeHTTP.started:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP runtime did not start")
	}
	require.Eventually(t, func() bool {
		record, err := ledger.Inspect(t.Context(), attempt.ID)
		return err == nil && record.State == reservation.Settled
	}, 3*time.Second, 10*time.Millisecond, "startup must resume durable measured usage without a new request")
	record, err = ledger.Inspect(t.Context(), attempt.ID)
	require.NoError(t, err)
	require.Equal(t, &evidence, record.Evidence)
	require.EqualValues(t, 200, *record.NanoUSD)
	window, err := ledger.Window(t.Context(), meter, now)
	require.NoError(t, err)
	require.Zero(t, window.Reserved)
	require.EqualValues(t, 200, window.Consumed)
}

func TestProductionSpendMissingTokenCountRetainsCapacity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			counts string
			state  reservation.State
		}{
			{`"completion_tokens":3,"total_tokens":3`, reservation.Uncertain},
			{`"prompt_tokens":null,"completion_tokens":3,"total_tokens":3`, reservation.Uncertain},
			{`"prompt_tokens":8,"total_tokens":8`, reservation.Uncertain},
			{`"prompt_tokens":0,"completion_tokens":0,"total_tokens":0`, reservation.Settled},
		} {
			t.Run(strconv.FormatBool(stream)+"/"+test.counts, func(t *testing.T) {
				fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true,
					&limits.Limits{Spend: &limits.Budget{Limit: 100_000_000, Interval: limits.IntervalDay}},
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, `data: {"id":"usage-presence","choices":[{"index":0,"delta":{"content":"hello"}}],"usage":{`+test.counts+`,"prompt_tokens_details":{"cached_tokens":0}}}`+"\n\ndata: [DONE]\n\n")
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, `{"id":"usage-presence","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{`+test.counts+`,"prompt_tokens_details":{"cached_tokens":0}}}`)
						}
					}))
				body := `{"model":"openai/gpt-4o-mini","stream":` + strconv.FormatBool(stream) + `,"max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`
				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/chat/completions", strings.NewReader(body))
				require.NoError(t, err)
				request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
				request.Header.Set("Content-Type", "application/json")
				response, err := fixture.client.Do(request)
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusOK, response.StatusCode)
				keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
				require.NoError(t, err)
				require.Len(t, keys, 1)
				data, err := fixture.application.store.Get(t.Context(), keys[0])
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(data, &record))
				require.Equal(t, test.state, record.State)
				if test.state == reservation.Uncertain {
					require.Nil(t, record.Evidence)
					require.Greater(t, *record.NanoUSD, int64(1_000_000))
				} else {
					require.NotNil(t, record.Evidence)
					require.Zero(t, *record.NanoUSD)
				}
			})
		}
	}
}

type budgetDispatchObservation struct {
	At         time.Time
	Operations budgetOperationSample
}

type budgetTimingSample struct {
	BeforeProviderNS int64                 `json:"client_start_to_provider_ns"`
	ResponseNS       int64                 `json:"client_response_complete_ns"`
	Admission        budgetOperationSample `json:"admission"`
	Settlement       budgetOperationSample `json:"settlement"`
}

type budgetProcessMemory struct {
	AllocatedBytes uint64 `json:"allocated_bytes"`
	Allocations    uint64 `json:"allocations"`
	HeapBefore     uint64 `json:"heap_alloc_before_bytes"`
	HeapAfter      uint64 `json:"heap_alloc_after_bytes"`
}

func budgetMeasurementCount(t *testing.T) int {
	t.Helper()
	count := 1
	if value := os.Getenv("STARPORT_BUDGET_PROFILE_SAMPLES"); value != "" {
		parsed, err := strconv.Atoi(value)
		require.NoError(t, err)
		require.GreaterOrEqual(t, parsed, 1)
		require.LessOrEqual(t, parsed, 1000)
		count = parsed
	}
	return count
}

// writeBudgetMeasurement records the complete instrumented request and its ledger footprint.
// The encoded footprint excludes backend prefixes, indexes, allocator overhead, and replication.
func writeBudgetMeasurement(t *testing.T, application *App, samples []budgetTimingSample, requests int, backgroundQueries int64, memory budgetProcessMemory) {
	t.Helper()
	path := os.Getenv("STARPORT_BUDGET_PROFILE_REPORT")
	if path == "" {
		return
	}
	bound := 4*requests + 64
	keys, err := application.store.ScanWithPrefix(t.Context(), "budget:v1:", bound)
	require.NoError(t, err)
	require.Less(t, len(keys), bound, "the ledger footprint must include every scoped key")
	var encodedBytes int64
	for _, key := range keys {
		value, err := application.store.Get(t.Context(), key)
		require.NoError(t, err)
		encodedBytes += int64(len(key) + len(value))
	}
	snapshot := application.catalog.Current()
	report := map[string]any{
		"schema_version": 1, "qualification": "UNVERIFIED", "profile": "fleet-budget-sequence-diagnostic-v1",
		"go_version": runtime.Version(), "os": runtime.GOOS, "architecture": runtime.GOARCH,
		"gomaxprocs": runtime.GOMAXPROCS(0), "concurrency": 1, "replicas": 1,
		"catalog_generation": snapshot.GenerationID(), "catalog_checksum": snapshot.PayloadChecksum(),
		"catalog_routes": len(snapshot.Routes()), "settled_attempts": requests, "meters_per_attempt": 5,
		"storage": "native Valkey and PostgreSQL", "provider": "loopback HTTP fixture",
		"load": "one warmup followed by sequential requests", "response_cache": "disabled",
		"maintenance": "App.Run loops are not started; catalog runtime background work remains active", "warm_samples": samples,
		"background_sql_queries_during_samples":    backgroundQueries,
		"whole_test_process_memory_during_samples": memory,
		"ledger_keys": len(keys), "ledger_encoded_key_value_bytes": encodedBytes,
		"limitations": "Timing and allocation counts include the client, loopback provider, and test instrumentation. The ledger size excludes backend prefixes, indexes, allocator overhead, and replication. This diagnostic does not qualify production latency, gateway allocation limits, peak process memory, or fleet capacity.",
	}
	data, err := json.Marshal(report)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
}
