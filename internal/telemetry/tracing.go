package telemetry

import (
	"context"
	"net/http"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Span names the tracer owns. Every span a request produces carries one of
// these; no other package names a span.
const (
	SpanRequest      = "starport.request"
	SpanRoutePlan    = "starport.route_plan"
	SpanAttempt      = "starport.attempt"
	SpanProviderCall = "starport.provider_call"
)

// Span attribute keys the tracer owns.
const (
	AttrProvider   = "starport.provider"
	AttrModel      = "starport.model"
	AttrAttempt    = "starport.attempt_number"
	AttrOverheadMS = "starport.overhead_ms"
	AttrTTFTMS     = "starport.ttft_ms"
)

// TracesConfigured reports whether the standard OpenTelemetry environment
// names an OTLP endpoint. The exporter itself reads the same variables, so
// this answers only the on-or-off question.
func TracesConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// Tracing owns the tracer a deployment exports spans through. A nil *Tracing
// starts no spans, extracts nothing, and shuts down cleanly, so every caller
// may hold one unconditionally.
type Tracing struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	shutdown   func(context.Context) error
}

// NewTracing builds a tracer that exports over OTLP HTTP. The exporter reads
// the standard OTEL_EXPORTER_OTLP_ENDPOINT variables itself; call this only
// when TracesConfigured reports true, so an unconfigured deployment never
// dials a default endpoint.
func NewTracing(ctx context.Context) (*Tracing, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL, semconv.ServiceName("starport"),
		)),
	)
	return newTracing(provider), nil
}

// NewTracingWithExporter builds a tracer on a synchronous exporter. Tests use
// it with an in-memory exporter to read finished spans deterministically.
func NewTracingWithExporter(exporter sdktrace.SpanExporter) *Tracing {
	return newTracing(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)))
}

func newTracing(provider *sdktrace.TracerProvider) *Tracing {
	return &Tracing{
		tracer:     provider.Tracer("starport"),
		propagator: propagation.TraceContext{},
		shutdown:   provider.Shutdown,
	}
}

// Shutdown flushes and stops the exporter.
func (t *Tracing) Shutdown(ctx context.Context) error {
	if t == nil || t.shutdown == nil {
		return nil
	}
	return t.shutdown(ctx)
}

// Extract reads inbound W3C trace context, so a span this gateway starts
// continues the caller's trace.
func (t *Tracing) Extract(ctx context.Context, header http.Header) context.Context {
	if t == nil {
		return ctx
	}
	return t.propagator.Extract(ctx, propagation.HeaderCarrier(header))
}

type tracingContextKey struct{}

// ContextWithTracing carries the tracer into the request context, where the
// routing and execution seams start their spans without compositional wiring.
func ContextWithTracing(ctx context.Context, t *Tracing) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, tracingContextKey{}, t)
}

// noopSpan is what StartSpan hands back when no tracer rides the context. It
// is a shared empty struct, so the disabled path allocates nothing.
var noopSpan = noop.Span{}

// StartSpan starts a named span under the context's tracer. Without one, it
// returns the context unchanged and a span that records nothing.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	t, ok := ctx.Value(tracingContextKey{}).(*Tracing)
	if !ok || t == nil {
		return ctx, noopSpan
	}
	return t.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// AnnotateSpanTimings attaches the gateway-owned latency measurements to the
// span already on the context. A zero value carries no information, so only
// positive measurements land.
func AnnotateSpanTimings(ctx context.Context, overheadMS, ttftMS int64) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	if overheadMS > 0 {
		span.SetAttributes(attribute.Int64(AttrOverheadMS, overheadMS))
	}
	if ttftMS > 0 {
		span.SetAttributes(attribute.Int64(AttrTTFTMS, ttftMS))
	}
}
