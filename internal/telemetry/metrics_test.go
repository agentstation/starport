package telemetry

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/agentstation/starport/internal/usage"
)

func TestObserveUsageCountsRequestTokensAndCost(t *testing.T) {
	m := NewMetrics()
	m.ObserveUsage(usage.Record{
		Protocol:  "openai",
		Operation: usage.OperationChat,
		Provider:  "openrouter",
		ModelUsed: "openai/gpt-5",
		Status:    usage.StatusOK,
		LatencyMS: 1200,
		TTFTMS:    250,
		Tokens:    usage.Tokens{Input: 100, Output: 40},
		Cost:      &usage.Cost{NanoUSD: 1500, Currency: "USD"},
	})

	requests := testutil.ToFloat64(m.requests.WithLabelValues("openai", usage.OperationChat, "openrouter", "openai/gpt-5", usage.StatusOK))
	if requests != 1 {
		t.Errorf("requests counter = %v, want 1", requests)
	}
	input := testutil.ToFloat64(m.tokens.WithLabelValues("openrouter", "openai/gpt-5", "input"))
	output := testutil.ToFloat64(m.tokens.WithLabelValues("openrouter", "openai/gpt-5", "output"))
	if input != 100 || output != 40 {
		t.Errorf("token counters = %v input, %v output, want 100 and 40", input, output)
	}
	cost := testutil.ToFloat64(m.costNanoUSD.WithLabelValues("openrouter", "openai/gpt-5"))
	if cost != 1500 {
		t.Errorf("cost counter = %v, want 1500", cost)
	}
}

// A refusal that never routed carries no provider and no served model. The
// scrape must label it "none" rather than register an empty label value.
func TestObserveUsageLabelsAnUnroutedRequestNone(t *testing.T) {
	m := NewMetrics()
	m.ObserveUsage(usage.Record{
		Protocol:       "openai",
		Operation:      usage.OperationChat,
		ModelRequested: "gpt-5",
		Status:         usage.StatusError,
		ErrorClass:     "provider_unavailable",
	})

	requests := testutil.ToFloat64(m.requests.WithLabelValues("openai", usage.OperationChat, unlabeled, "gpt-5", usage.StatusError))
	if requests != 1 {
		t.Errorf("requests counter = %v, want 1", requests)
	}
	failures := testutil.ToFloat64(m.providerFailures.WithLabelValues(unlabeled, "provider_unavailable"))
	if failures != 1 {
		t.Errorf("failure counter = %v, want 1", failures)
	}
}

func TestObserveUsageCountsACacheHit(t *testing.T) {
	m := NewMetrics()
	m.ObserveUsage(usage.Record{
		Operation:   usage.OperationChat,
		Status:      usage.StatusOK,
		CacheStatus: cacheStatusHit,
	})
	hits := testutil.ToFloat64(m.cacheHits.WithLabelValues(usage.OperationChat))
	if hits != 1 {
		t.Errorf("cache hit counter = %v, want 1", hits)
	}
}

func TestObserveBudgetRefusalCounts(t *testing.T) {
	m := NewMetrics()
	m.ObserveBudgetRefusal("account", "spend")
	m.ObserveBudgetRefusal("account", "spend")
	refusals := testutil.ToFloat64(m.budgetRefusals.WithLabelValues("account", "spend"))
	if refusals != 2 {
		t.Errorf("budget refusal counter = %v, want 2", refusals)
	}
}

// Every seam holds a *Metrics unconditionally, so the nil receiver must
// observe nothing and serve a plain not-found rather than panic.
func TestNilMetricsObservesAndServesNothing(t *testing.T) {
	var m *Metrics
	m.ObserveUsage(usage.Record{Status: usage.StatusOK})
	m.ObserveBudgetRefusal("account", "spend")

	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if recorder.Code != 404 {
		t.Errorf("nil handler status = %d, want 404", recorder.Code)
	}
}

func TestHandlerServesThePrometheusTextFormat(t *testing.T) {
	m := NewMetrics()
	m.ObserveUsage(usage.Record{
		Protocol:  "openai",
		Operation: usage.OperationChat,
		Provider:  "openrouter",
		ModelUsed: "openai/gpt-5",
		Status:    usage.StatusOK,
		Tokens:    usage.Tokens{Input: 10, Output: 5},
	})

	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if recorder.Code != 200 {
		t.Fatalf("scrape status = %d, want 200", recorder.Code)
	}
	body, err := io.ReadAll(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"starport_requests_total", "starport_tokens_total"} {
		if !strings.Contains(text, want) {
			t.Errorf("scrape is missing %q", want)
		}
	}
}

func TestObserveUsageExportDropsCounts(t *testing.T) {
	m := NewMetrics()
	m.ObserveUsageExportDrops(3)
	m.ObserveUsageExportDrops(0)
	if got := testutil.ToFloat64(m.usageExportDrops); got != 3 {
		t.Errorf("usage export drop counter = %v, want 3", got)
	}

	var nilMetrics *Metrics
	nilMetrics.ObserveUsageExportDrops(1)
}

func TestObserveWebhookDeadLettersCounts(t *testing.T) {
	m := NewMetrics()
	m.ObserveWebhookDeadLetters(2)
	m.ObserveWebhookDeadLetters(0)
	if got := testutil.ToFloat64(m.webhookDeadLetters); got != 2 {
		t.Errorf("webhook dead letter counter = %v, want 2", got)
	}

	var nilMetrics *Metrics
	nilMetrics.ObserveWebhookDeadLetters(1)
}
