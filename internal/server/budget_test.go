package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/usage"
)

// stubUsageTotals serves canned aggregate totals per interval.
type stubUsageTotals struct {
	totals usage.Totals
	err    error
}

func (s stubUsageTotals) Put(context.Context, usage.Record) error { return nil }

func (s stubUsageTotals) List(context.Context, usage.Query) (usage.Page, error) {
	return usage.Page{}, nil
}

func (s stubUsageTotals) Totals(context.Context, usage.Scope, string, time.Time) (usage.Totals, error) {
	return s.totals, s.err
}

func budgetTestRequest(apiKey *apikey.APIKey) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	ctx := requestctx.WithAPIKeyModel(req.Context(), apiKey)
	ctx = requestctx.WithAPIKeyID(ctx, apiKey.ID)
	return req.WithContext(ctx)
}

func budgetTestKey(limits *limits.Limits) *apikey.APIKey {
	return &apikey.APIKey{
		ID:     "key-budget",
		Name:   "budget-key",
		Scopes: []string{"*"},
		Limits: limits,
		Active: true,
	}
}

func TestSpendBudgetExhaustionReturns402(t *testing.T) {
	server := &Server{
		cfg: &Config{},
		usage: stubUsageTotals{
			totals: usage.Totals{Requests: 10, Tokens: 500, SpendNanoUSD: 2_000_000_000},
		},
	}

	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	apiKey := budgetTestKey(&limits.Limits{
		Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(apiKey))

	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.Contains(t, rec.Body.String(), "Insufficient quota")
	assert.Contains(t, rec.Body.String(), "spend budget exhausted")
	assert.Equal(t, "1000000000", rec.Header().Get("X-Starport-Budget-Spend-Limit"))
	assert.Equal(t, "0", rec.Header().Get("X-Starport-Budget-Spend-Remaining"))
	assert.NotEmpty(t, rec.Header().Get("X-Starport-Budget-Spend-Reset"))
}

func TestTokenBudgetExhaustionReturns402(t *testing.T) {
	server := &Server{
		cfg: &Config{},
		usage: stubUsageTotals{
			totals: usage.Totals{Requests: 10, Tokens: 100_000, SpendNanoUSD: 0},
		},
	}

	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	apiKey := budgetTestKey(&limits.Limits{
		Tokens: &limits.Budget{Limit: 100_000, Interval: limits.IntervalMonth},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(apiKey))

	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.Contains(t, rec.Body.String(), "Insufficient quota")
	assert.Contains(t, rec.Body.String(), "token budget exhausted")
	assert.Equal(t, "100000", rec.Header().Get("X-Starport-Budget-Tokens-Limit"))
	assert.Equal(t, "0", rec.Header().Get("X-Starport-Budget-Tokens-Remaining"))
}

func TestBudgetWithinLimitAllowsAndReportsRemaining(t *testing.T) {
	server := &Server{
		cfg: &Config{},
		usage: stubUsageTotals{
			totals: usage.Totals{Requests: 2, Tokens: 100, SpendNanoUSD: 400_000_000},
		},
	}

	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	apiKey := budgetTestKey(&limits.Limits{
		Spend:  &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay},
		Tokens: &limits.Budget{Limit: 1_000, Interval: limits.IntervalWeek},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(apiKey))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "600000000", rec.Header().Get("X-Starport-Budget-Spend-Remaining"))
	assert.Equal(t, "900", rec.Header().Get("X-Starport-Budget-Tokens-Remaining"))
}

func TestBudgetStorageErrorFailsOpen(t *testing.T) {
	server := &Server{
		cfg:   &Config{},
		usage: stubUsageTotals{err: errors.New("aggregate read failed")},
	}

	called := false
	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	apiKey := budgetTestKey(&limits.Limits{
		Spend: &limits.Budget{Limit: 1, Interval: limits.IntervalDay},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(apiKey))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called, "storage failure must fail open")
}

func TestBudgetMiddlewarePassesKeysWithoutBudgets(t *testing.T) {
	server := &Server{
		cfg:   &Config{},
		usage: stubUsageTotals{},
	}

	called := false
	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(budgetTestKey(nil)))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called)
}
