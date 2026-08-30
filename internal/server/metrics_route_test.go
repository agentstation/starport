package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/telemetry"
	"github.com/agentstation/starport/internal/usage"
)

func serveMetrics(server *Server) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder
}

// The default mode serves the scrape without credentials, the way the health
// checks do: the labels carry no caller identity.
func TestMetricsRouteServesThePrometheusScrape(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	server.telemetry.ObserveUsage(usage.Record{
		Protocol:  "openai",
		Operation: usage.OperationChat,
		Provider:  "openrouter",
		ModelUsed: "openai/gpt-5",
		Status:    usage.StatusOK,
		Tokens:    usage.Tokens{Input: 12, Output: 3},
	})

	recorder := serveMetrics(server)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	body := recorder.Body.String()
	assert.Contains(t, body, "starport_requests_total")
	assert.Contains(t, body, "starport_tokens_total")
}

func TestMetricsRouteOffModeRemovesTheRoute(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20, MetricsMode: MetricsModeOff})
	recorder := serveMetrics(server)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestMetricsRouteAdminModeRequiresTheAdminScope(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20, MetricsMode: MetricsModeAdmin})

	unauthenticated := serveMetrics(server)
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code)

	chatOnly := createServerAPIKey(t, server, "metrics-chat", []string{"chat:write"})
	forbidden := serveAuthorized(server, http.MethodGet, "/metrics", chatOnly, context.Background())
	require.Equal(t, http.StatusForbidden, forbidden.Code)

	admin := createServerAPIKey(t, server, "metrics-admin", []string{"admin"})
	allowed := serveAuthorized(server, http.MethodGet, "/metrics", admin, context.Background())
	require.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())
}

// A refused request writes no usage record, so the refusal has to reach the
// scrape from the budget middleware itself.
func TestBudgetRefusalAppearsOnTheScrape(t *testing.T) {
	server := &Server{
		cfg: &Config{},
		usage: stubUsageTotals{
			totals: usage.Totals{Requests: 10, Tokens: 500, SpendNanoUSD: 2_000_000_000},
		},
		telemetry: telemetry.NewMetrics(),
	}
	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	apiKey := budgetTestKey(&limits.Limits{
		Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay},
	})

	refusal := httptest.NewRecorder()
	handler.ServeHTTP(refusal, budgetTestRequest(apiKey))
	require.Equal(t, http.StatusPaymentRequired, refusal.Code)

	scrape := httptest.NewRecorder()
	server.telemetry.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, scrape.Code)
	assert.Contains(t, scrape.Body.String(), `starport_budget_refusals_total{dimension="spend",scope="key"} 1`)
}
