package app

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func budgetDispatch(t *testing.T, fixture *performanceFixture) int {
	t.Helper()
	status, body, err := budgetRequest(t.Context(), fixture)
	require.NoError(t, err)
	t.Logf("gateway status=%d body=%s", status, body)
	return status
}

func budgetRequest(ctx context.Context, fixture *performanceFixture) (int, string, error) {
	return budgetRequestForModel(ctx, fixture, "openai/gpt-4o-mini")
}

func budgetRequestForModel(ctx context.Context, fixture *performanceFixture, model string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixture.gateway.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":`+strconv.Quote(model)+`,"max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response.StatusCode, string(body), err
}

func budgetReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"id":"budget-fixture","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`)
}

func TestProductionBudgetDispatchReservesConcurrentCapacity(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true,
		&limits.Limits{Tokens: &limits.Budget{Limit: 200_000, Interval: limits.IntervalDay}},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(entered) })
			select {
			case <-release:
				budgetReply(w)
			case <-r.Context().Done():
			}
		}))
	offering, err := fixture.application.catalog.Current().Catalog().Offering(catalogs.ProviderIDOpenAI, "gpt-4o-mini")
	require.NoError(t, err)
	t.Logf("catalog token limits: %+v", offering.Limits)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	type result struct {
		status int
		body   string
		err    error
	}
	first := make(chan result, 1)
	go func() {
		status, body, err := budgetRequest(t.Context(), fixture)
		first <- result{status, body, err}
	}()
	select {
	case <-entered:
	case got := <-first:
		t.Fatalf("first request stopped before dispatch: status=%d body=%s error=%v", got.status, got.body, got.err)
	case <-time.After(10 * time.Second):
		t.Fatal("first request did not reach the real provider connector")
	}
	require.Equal(t, http.StatusPaymentRequired, budgetDispatch(t, fixture))
	require.EqualValues(t, 1, fixture.calls.Load())
	close(release)
	got := <-first
	require.NoError(t, got.err)
	require.Equal(t, http.StatusOK, got.status, got.body)
	require.Equal(t, http.StatusOK, budgetDispatch(t, fixture), "settled measured usage must release unused capacity")
	require.EqualValues(t, 2, fixture.calls.Load())
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	for _, key := range keys {
		data, err := fixture.application.store.Get(t.Context(), key)
		require.NoError(t, err)
		var record reservation.Record
		require.NoError(t, json.Unmarshal(data, &record))
		require.Equal(t, reservation.Settled, record.State)
		require.EqualValues(t, 11, record.Evidence.Tokens)
		require.Nil(t, record.NanoUSD)
	}
}

func TestProductionBudgetDispatchUnknownCostAndAbsentBudgets(t *testing.T) {
	for _, required := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed-absent", true: "unknown-spend-bound"}[required], func(t *testing.T) {
			var budgets *limits.Limits
			if required {
				budgets = &limits.Limits{Spend: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}}
			}
			fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true, budgets,
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { budgetReply(w) }))
			// This offering has prices but no complete text-chat billing declaration.
			status, _, requestErr := budgetRequestForModel(t.Context(), fixture, "openai/gpt-4o")
			require.NoError(t, requestErr)
			if required {
				require.Equal(t, http.StatusServiceUnavailable, status)
				require.Zero(t, fixture.calls.Load())
			} else {
				require.Equal(t, http.StatusOK, status)
				require.EqualValues(t, 1, fixture.calls.Load())
			}
			keys, err := fixture.application.store.ScanWithPrefix(context.Background(), "budget:v1:attempt:", 100)
			require.NoError(t, err)
			require.Empty(t, keys)
		})
	}
}

func TestProductionBudgetDispatchStreamCompletionAndCancellation(t *testing.T) {
	for _, mode := range []string{"complete", "partial-cancel", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			complete := mode == "complete"
			fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true,
				&limits.Limits{Tokens: &limits.Budget{Limit: 200_000, Interval: limits.IntervalDay}},
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"id\":\"budget-stream\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":1,\"total_tokens\":9}}\n\n")
					_ = http.NewResponseController(w).Flush()
					if complete {
						_, _ = io.WriteString(w, "data: {\"id\":\"budget-stream\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}\n\ndata: [DONE]\n\n")
					} else if mode == "partial-cancel" {
						<-r.Context().Done()
					}
				}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixture.gateway.URL+"/v1/chat/completions",
				strings.NewReader(`{"model":"openai/gpt-4o-mini","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			request.Header.Set("Content-Type", "application/json")
			response, err := fixture.client.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
			if mode != "partial-cancel" {
				_, err = io.Copy(io.Discard, response.Body)
				require.NoError(t, err)
			} else {
				_, err = bufio.NewReader(response.Body).ReadString('\n')
				require.NoError(t, err)
				cancel()
				require.NoError(t, response.Body.Close())
			}
			var record reservation.Record
			require.Eventually(t, func() bool {
				keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
				if err != nil || len(keys) != 1 {
					return false
				}
				data, err := fixture.application.store.Get(t.Context(), keys[0])
				if err != nil || json.Unmarshal(data, &record) != nil {
					return false
				}
				return record.State == reservation.Settled || record.State == reservation.Uncertain
			}, 3*time.Second, 10*time.Millisecond)
			if complete {
				require.Equal(t, reservation.Settled, record.State)
				require.EqualValues(t, 11, record.Evidence.Tokens)
			} else {
				require.Equal(t, reservation.Uncertain, record.State)
				require.Nil(t, record.Evidence)
				require.NotEqual(t, http.StatusOK, budgetDispatch(t, fixture))
				require.EqualValues(t, 1, fixture.calls.Load(), "partial usage must not release capacity")
			}
		})
	}
}

func TestProductionSpendSettlementUsesDeclaredClasses(t *testing.T) {
	for _, test := range []struct {
		name, details string
		state         reservation.State
	}{
		{"measured-cache", `,"prompt_tokens_details":{"cached_tokens":4}`, reservation.Settled},
		{"missing-cache", "", reservation.Uncertain},
		{"missing-count", `,"prompt_tokens_details":{}`, reservation.Uncertain},
		{"invalid-cache", `,"prompt_tokens_details":{"cached_tokens":9}`, reservation.Uncertain},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true, &limits.Limits{Spend: &limits.Budget{Limit: 100_000_000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"spend-fixture","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11`+test.details+`}}`)
			}))
			require.Equal(t, http.StatusOK, budgetDispatch(t, fixture))
			require.EqualValues(t, 1, fixture.calls.Load())
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			data, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			var record reservation.Record
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(t, test.state, record.State)
			if test.state == reservation.Settled {
				require.EqualValues(t, 2700, *record.NanoUSD)
				require.EqualValues(t, 4, record.Evidence.Quantities["cache_read"])
			} else {
				require.Nil(t, record.Evidence)
				require.Greater(t, *record.NanoUSD, int64(2700))
			}
		})
	}
}

func TestProductionSpendRefusesInsufficientReservation(t *testing.T) {
	fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true, &limits.Limits{Spend: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { budgetReply(w) }))
	require.Equal(t, http.StatusPaymentRequired, budgetDispatch(t, fixture))
	require.Zero(t, fixture.calls.Load(), "a small expected cost cannot replace the required reservation")
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 100)
	require.NoError(t, err)
	require.Empty(t, keys)
}
