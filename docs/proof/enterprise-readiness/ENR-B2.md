# ENR-B2 proof: OpenTelemetry traces

Date: 2026-08-30. Branch: `codex/enr-b2`.

## What shipped

- `internal/telemetry` owns the tracer, the four span names, and the span
  attribute keys. The names are `starport.request`, `starport.route_plan`,
  `starport.attempt`, and `starport.provider_call`.
- The tracer rides the request context through `ContextWithTracing` and
  `StartSpan`. The routing and execution seams start their spans without
  compositional wiring.
- The disabled path allocates nothing. Without a tracer on the context,
  `StartSpan` returns the context unchanged and a shared no-op span.
- The OTLP HTTP exporter builds only when the standard OpenTelemetry
  environment names an endpoint. The loader reads
  `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` first and
  `OTEL_EXPORTER_OTLP_ENDPOINT` second. These names stay unprefixed
  because they are the cross-vendor contract.
- The server middleware starts the request span and continues inbound W3C
  `traceparent` headers. Usage capture annotates the request span with
  `starport.overhead_ms` and `starport.ttft_ms` on both the unary and the
  streaming path.
- The provider-call span names the provider and the model. The attempt span
  numbers attempts from 1. A provider failure lands on its span as a
  recorded error.
- `docs/OPERATOR-GUIDE.md` gained a Distributed Traces section that names
  the endpoint variables, the four spans, and the privacy rule.

## Acceptance evidence

Named tests, all green:

- `internal/telemetry`: `TestStartSpanWithoutTracerIsNoop` and
  `TestNilTracingIsNoopEverywhere` hold the disabled path.
  `TestStartSpanExportsThroughConfiguredTracer`,
  `TestAnnotateSpanTimingsSkipsZeroValues`,
  `TestExtractContinuesInboundTrace`, and
  `TestTracesConfiguredReadsStandardEnvironment` hold the configured path.
- `internal/router`: `TestRouteWithFallbackProducesFourSpansInParentOrder`
  proves one chat request against an in-memory exporter yields the four
  named spans. It checks the parent order: request over route_plan, and
  request over attempt over provider_call. It also checks the provider,
  model, and attempt-number attributes.
  `TestRouteWithFallbackWithoutTracerExportsNothing` holds the untraced
  path.
- `internal/server`:
  `TestTracingMiddlewareStartsRequestSpanAndContinuesInboundTrace` proves
  the middleware continues an inbound trace ID and stamps the response
  status. `TestTracingMiddlewareWithNilTracerLeavesHandlerUntouched` holds
  the nil path.
- `internal/config`: `TestLoaderReadsStandardOTLPEndpoint` proves the
  specific traces variable beats the general one, and that no environment
  leaves the field empty.

## Commands

- `go test ./...`: pass, no failures.
- `go vet ./...`: clean.
- `make lint`: 0 issues.
- `go build ./...`: clean.
- `bash scripts/benchmark-overhead.sh`: pass, p50=0ms p99=0ms over 200
  requests.
- `bash scripts/verify-doc-links.sh`: PASS.
- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 4 passed, 29
  failed`. ENR-V01 through ENR-V04 are the green conditions, which is the
  exact phase-B2 target.

## Scope notes

- The stream provider-call span covers stream establishment: the connector
  call plus the first receive. Later chunks arrive after the routing seam
  returns, so they fall outside the span.
- `env:"-"` is not a valid go-envconfig ignore tag and fails the whole
  decode. The `TracesEndpoint` field therefore carries no env tag at all.
- Dependencies added: `go.opentelemetry.io/otel/sdk` v1.46.0 and
  `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
  v1.46.0.
