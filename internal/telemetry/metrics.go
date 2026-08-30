// Package telemetry owns the gateway's observability export vocabulary: the
// Prometheus metric names, their labels, and the mapping from a completed
// request's usage record onto them. Other seams record their existing
// measurements through this package; none of them names a metric itself.
//
// Labels never carry a caller identity. A metric labeled by account or key
// would leak per-tenant activity to every scraper and grow without bound, so
// the label set stops at provider, model, protocol, operation, and outcome.
package telemetry

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/agentstation/starport/internal/usage"
)

// unlabeled stands in for a label a record could not name: a request that
// never reached a provider has no provider, and a refusal has no model.
const unlabeled = "none"

// cacheStatusHit is the proxy's served-from-cache verdict. The string is part
// of the usage record contract; TestObserveUsageCountsACacheHit holds it.
const cacheStatusHit = "HIT"

// millisecond converts one recorded millisecond count to seconds, which is
// the base unit Prometheus histograms are named in.
const millisecond = 1e-3

// Label names the vocabulary shares across instruments. Labels never carry an
// account, a key, or any other caller identity.
const (
	labelProvider  = "provider"
	labelModel     = "model"
	labelOperation = "operation"
)

// Metrics is the gateway's Prometheus surface. A nil *Metrics observes
// nothing and serves nothing, so every caller may hold one unconditionally.
type Metrics struct {
	registry *prometheus.Registry

	requests           *prometheus.CounterVec
	tokens             *prometheus.CounterVec
	costNanoUSD        *prometheus.CounterVec
	duration           *prometheus.HistogramVec
	timeToFirstToken   *prometheus.HistogramVec
	overhead           prometheus.Histogram
	cacheHits          *prometheus.CounterVec
	providerFailures   *prometheus.CounterVec
	budgetRefusals     *prometheus.CounterVec
	usageExportDrops   prometheus.Counter
	webhookDeadLetters prometheus.Counter
}

// NewMetrics builds the metric vocabulary on its own registry. The gateway's
// scrape serves exactly what the gateway registered: no process collectors
// arrive by side effect, and two instances never collide in a shared default.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "starport_requests_total",
			Help: "Completed inference requests by protocol, operation, provider, model, and outcome.",
		}, []string{"protocol", labelOperation, labelProvider, labelModel, "outcome"}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "starport_tokens_total",
			Help: "Tokens consumed by provider, model, and direction.",
		}, []string{labelProvider, labelModel, "direction"}),
		costNanoUSD: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "starport_cost_nanousd_total",
			Help: "Derived request cost in nano-USD by provider and model.",
		}, []string{labelProvider, labelModel}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "starport_request_duration_seconds",
			Help:    "End-to-end request latency by operation and outcome.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60, 120},
		}, []string{labelOperation, "outcome"}),
		timeToFirstToken: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "starport_time_to_first_token_seconds",
			Help:    "Streaming time to first token by provider and model.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}, []string{labelProvider, labelModel}),
		overhead: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "starport_gateway_overhead_seconds",
			Help:    "Gateway-added latency outside the provider call.",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 1},
		}),
		cacheHits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "starport_cache_hits_total",
			Help: "Requests served from the response cache by operation.",
		}, []string{labelOperation}),
		providerFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "starport_provider_failures_total",
			Help: "Failed requests by provider and normalized error class.",
		}, []string{labelProvider, "error_class"}),
		budgetRefusals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "starport_budget_refusals_total",
			Help: "Requests refused pre-flight by budget scope and dimension.",
		}, []string{"scope", "dimension"}),
		usageExportDrops: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "starport_usage_export_dropped_total",
			Help: "Usage records the export sink dropped because its target stayed unreachable or its buffer filled.",
		}),
		webhookDeadLetters: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "starport_webhook_dead_letters_total",
			Help: "Webhook events that never delivered: every attempt failed, or the queue was full.",
		}),
	}
	m.registry.MustRegister(
		m.requests, m.tokens, m.costNanoUSD, m.duration, m.timeToFirstToken,
		m.overhead, m.cacheHits, m.providerFailures, m.budgetRefusals,
		m.usageExportDrops, m.webhookDeadLetters,
	)
	return m
}

// Handler serves the scrape in the Prometheus text exposition format.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ObserveUsage maps one completed request's usage record onto the metric
// vocabulary. The record already carries every measurement the proxy and
// execution seams took, so this is the one call a request path makes.
func (m *Metrics) ObserveUsage(record usage.Record) {
	if m == nil {
		return
	}
	provider := orUnlabeled(record.Provider)
	model := orUnlabeled(record.ModelUsed)
	if record.ModelUsed == "" {
		model = orUnlabeled(record.ModelRequested)
	}
	protocol := orUnlabeled(record.Protocol)
	operation := orUnlabeled(record.Operation)
	outcome := orUnlabeled(record.Status)

	m.requests.WithLabelValues(protocol, operation, provider, model, outcome).Inc()
	m.duration.WithLabelValues(operation, outcome).Observe(float64(record.LatencyMS) * millisecond)

	if record.Tokens.Input > 0 {
		m.tokens.WithLabelValues(provider, model, "input").Add(float64(record.Tokens.Input))
	}
	if record.Tokens.Output > 0 {
		m.tokens.WithLabelValues(provider, model, "output").Add(float64(record.Tokens.Output))
	}
	if record.Cost != nil && record.Cost.NanoUSD > 0 {
		m.costNanoUSD.WithLabelValues(provider, model).Add(float64(record.Cost.NanoUSD))
	}
	if record.TTFTMS > 0 {
		m.timeToFirstToken.WithLabelValues(provider, model).Observe(float64(record.TTFTMS) * millisecond)
	}
	if record.OverheadMS > 0 {
		m.overhead.Observe(float64(record.OverheadMS) * millisecond)
	}
	if record.CacheStatus == cacheStatusHit {
		m.cacheHits.WithLabelValues(operation).Inc()
	}
	if record.Status == usage.StatusError && record.ErrorClass != "" {
		m.providerFailures.WithLabelValues(provider, record.ErrorClass).Inc()
	}
}

// ObserveBudgetRefusal counts one pre-flight budget refusal. It stands apart
// from ObserveUsage because a refused request never reaches the proxy and
// writes no usage record.
func (m *Metrics) ObserveBudgetRefusal(scope, dimension string) {
	if m == nil {
		return
	}
	m.budgetRefusals.WithLabelValues(orUnlabeled(scope), orUnlabeled(dimension)).Inc()
}

// ObserveUsageExportDrops counts records the export sink could not deliver.
// The counter carries no labels: a drop is a gap in the analytics copy, and
// which target dropped it is already fixed by configuration.
func (m *Metrics) ObserveUsageExportDrops(count int) {
	if m == nil || count <= 0 {
		return
	}
	m.usageExportDrops.Add(float64(count))
}

// ObserveWebhookDeadLetters counts events the dispatcher could not deliver.
// Like the export counter, it carries no labels: the endpoint set is fixed
// by configuration.
func (m *Metrics) ObserveWebhookDeadLetters(count int) {
	if m == nil || count <= 0 {
		return
	}
	m.webhookDeadLetters.Add(float64(count))
}

func orUnlabeled(value string) string {
	if value == "" {
		return unlabeled
	}
	return value
}
