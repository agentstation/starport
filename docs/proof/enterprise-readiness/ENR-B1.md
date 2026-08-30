# ENR-B1 proof: Prometheus metrics

Date: 2026-08-29. Branch: `codex/enr-b1`.

## What shipped

- `internal/telemetry` owns the metric vocabulary: nine instruments on a
  dedicated Prometheus registry, with `starport_` names. A nil `*Metrics`
  observes nothing and serves nothing.
- `internal/proxy.UsageCapture` accepts observers. Its `submit` path notifies
  each observer synchronously before the asynchronous store write. Every
  completed request reaches the metric surface even when a store write drops.
- The server mounts `GET /metrics` next to the health checks. The
  `STARPORT_TELEMETRY_METRICS` switch selects `on` (default, no credentials),
  `admin` (requires the `admin` scope), or `off` (no route).
- The budget middleware counts each 402 refusal on
  `starport_budget_refusals_total`, because a refused request writes no usage
  record.
- Labels stop at protocol, operation, provider, model, and outcome. No label
  carries an account, a key, or any other caller identity.
- `docs/OPERATOR-GUIDE.md` gained a Prometheus Metrics section with scrape
  configuration for both the open and the admin mode.

## Acceptance evidence

Named tests, all green:

- `internal/telemetry`: `TestObserveUsageCountsRequestTokensAndCost`,
  `TestObserveUsageLabelsAnUnroutedRequestNone`,
  `TestObserveUsageCountsACacheHit`, `TestObserveBudgetRefusalCounts`,
  `TestNilMetricsObservesAndServesNothing`,
  `TestHandlerServesThePrometheusTextFormat`.
- `internal/proxy`: `TestUsageCaptureNotifiesObservers` proves the observer
  seam sees the exact record the repository stores, and that construction
  filters a nil observer.
- `internal/server`: `TestMetricsRouteServesThePrometheusScrape` proves a
  scrape after one observed chat record shows nonzero
  `starport_requests_total` and `starport_tokens_total`.
  `TestMetricsRouteOffModeRemovesTheRoute`,
  `TestMetricsRouteAdminModeRequiresTheAdminScope`, and
  `TestBudgetRefusalAppearsOnTheScrape` cover the switch and the refusal
  counter.

## Commands

- `go test ./...`: pass, no failures.
- `go vet ./...`: clean.
- `make lint`: 0 issues.
- `go build ./...`: clean.
- `bash scripts/benchmark-overhead.sh`: pass, p50=0ms p99=0ms over 200
  requests.
- `bash scripts/verify-doc-links.sh`: PASS.
- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 2 passed, 31
  failed`. ENR-V01 and ENR-V02 are the two green conditions, which is the
  exact phase-B1 target.

## Scope notes

- Job accounting records (`internal/proxy/job_accounting.go`) bypass
  `UsageCapture`, so asynchronous job settlements do not reach the counters
  yet. ENR-B1 scopes to the synchronous request path.
- The dependency added is `github.com/prometheus/client_golang v1.24.1`.
