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

// recordingEmitter keeps every event the middleware pushes. It records
// types and payloads rather than a count, because the property under test
// is what a webhook receiver would read.
type recordingEmitter struct {
	types    []string
	payloads []map[string]string
}

func (e *recordingEmitter) Emit(eventType string, data map[string]string) {
	e.types = append(e.types, eventType)
	e.payloads = append(e.payloads, data)
}

// A refusal is the moment an operator's pager cares about, so the refusal
// that writes the 402 pushes the one budget event. The payload names the
// holder and the meter and nothing else: no prompt, no credential.
func TestABudgetRefusalEmitsOneNamedEvent(t *testing.T) {
	emitter := &recordingEmitter{}
	server := &Server{
		cfg: &Config{},
		usage: stubUsageTotals{
			totals: usage.Totals{Requests: 10, Tokens: 500, SpendNanoUSD: 2_000_000_000},
		},
		events: emitter,
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
	require.Equal(t, []string{"budget.exhausted"}, emitter.types)
	payload := emitter.payloads[0]
	assert.Equal(t, "key", payload["scope"])
	assert.Equal(t, "spend", payload["dimension"])
	assert.Equal(t, string(limits.IntervalDay), payload["interval"])
	assert.Equal(t, "key-budget", payload["key_id"])
}

// An allowed request is not an incident. The emitter hears nothing.
func TestAnAllowedRequestEmitsNothing(t *testing.T) {
	emitter := &recordingEmitter{}
	server := &Server{
		cfg: &Config{},
		usage: stubUsageTotals{
			totals: usage.Totals{Requests: 2, Tokens: 100, SpendNanoUSD: 400_000_000},
		},
		events: emitter,
	}

	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	apiKey := budgetTestKey(&limits.Limits{
		Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(apiKey))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, emitter.types)
}

// teamBudgetServer builds a server whose identity plane answers one team's
// budget, over scope-sensitive usage counters.
func teamBudgetServer(budget *limits.TeamBudget, byScope map[usage.Scope]usage.Totals) *Server {
	return &Server{
		cfg:   &Config{},
		usage: scopedUsageTotals{byScope: byScope},
		teamBudgets: func(_ context.Context, teamID string) (*limits.TeamBudget, error) {
			if teamID == "team-platform" {
				return budget, nil
			}
			return nil, nil
		},
	}
}

func teamBudgetKey(id, teamID string) *apikey.APIKey {
	return &apikey.APIKey{
		ID:     id,
		Name:   id,
		TeamID: teamID,
		Scopes: []string{"*"},
		Active: true,
	}
}

// Two keys attributed to one team draw the same refusal once the team's
// spend counter passes the team budget, whether or not either key holds a
// budget of its own: the team meters a population neither key counter sees.
func TestTeamBudgetExhaustionRefusesEveryTeamKey(t *testing.T) {
	emitter := &recordingEmitter{}
	server := teamBudgetServer(
		&limits.TeamBudget{Limit: 1_000_000_000, Interval: limits.IntervalMonth},
		map[usage.Scope]usage.Totals{
			usage.TeamScope("team-platform"): {Requests: 20, Tokens: 900, SpendNanoUSD: 1_500_000_000},
		},
	)
	server.events = emitter

	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, keyID := range []string{"key-a", "key-b"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, budgetTestRequest(teamBudgetKey(keyID, "team-platform")))

		require.Equal(t, http.StatusPaymentRequired, rec.Code, keyID)
		assert.Contains(t, rec.Body.String(), "team spend budget exhausted", keyID)
		assert.Equal(t, "team", rec.Header().Get("X-Starport-Budget-Spend-Scope"), keyID)
		assert.Equal(t, "1000000000", rec.Header().Get("X-Starport-Budget-Spend-Limit"), keyID)
		assert.Equal(t, "0", rec.Header().Get("X-Starport-Budget-Spend-Remaining"), keyID)
	}

	require.Len(t, emitter.payloads, 2)
	payload := emitter.payloads[0]
	assert.Equal(t, "team", payload["scope"])
	assert.Equal(t, "spend", payload["dimension"])
	assert.Equal(t, "team-platform", payload["team_id"])
	assert.Equal(t, "key-a", payload["key_id"])
}

// A teamless key rides the same gateway untouched: no team is attributed, so
// no team meters it, however exhausted some team's budget is.
func TestTeamBudgetLeavesTeamlessKeysAlone(t *testing.T) {
	server := teamBudgetServer(
		&limits.TeamBudget{Limit: 1_000_000_000, Interval: limits.IntervalMonth},
		map[usage.Scope]usage.Totals{
			usage.TeamScope("team-platform"): {SpendNanoUSD: 1_500_000_000},
		},
	)

	called := false
	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(teamBudgetKey("key-c", "")))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called)
	assert.Empty(t, rec.Header().Get("X-Starport-Budget-Spend-Scope"))
}

// The team rule joins the key rule rather than replacing it, and the tightest
// meter owns the reported headers.
func TestTeamBudgetJoinsKeyBudgetAndTightestReports(t *testing.T) {
	server := teamBudgetServer(
		&limits.TeamBudget{Limit: 1_000_000_000, Interval: limits.IntervalMonth},
		map[usage.Scope]usage.Totals{
			usage.TeamScope("team-platform"): {SpendNanoUSD: 800_000_000},
			usage.KeyScope("key-a"):          {SpendNanoUSD: 100_000_000},
		},
	)

	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	apiKey := teamBudgetKey("key-a", "team-platform")
	apiKey.Limits = &limits.Limits{
		Spend: &limits.Budget{Limit: 2_000_000_000, Interval: limits.IntervalMonth},
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(apiKey))

	require.Equal(t, http.StatusOK, rec.Code)
	// Team remaining is 200M; key remaining is 1900M. The team is tighter.
	assert.Equal(t, "team", rec.Header().Get("X-Starport-Budget-Spend-Scope"))
	assert.Equal(t, "200000000", rec.Header().Get("X-Starport-Budget-Spend-Remaining"))
}

// A team budget read failure meters nothing and allows the request: the same
// fail-open answer a broken usage read gives (D6).
func TestTeamBudgetReadErrorFailsOpen(t *testing.T) {
	server := &Server{
		cfg:   &Config{},
		usage: scopedUsageTotals{},
		teamBudgets: func(context.Context, string) (*limits.TeamBudget, error) {
			return nil, errors.New("identity storage unreachable")
		},
	}

	called := false
	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, budgetTestRequest(teamBudgetKey("key-a", "team-platform")))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called, "a team budget read failure must fail open")
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
