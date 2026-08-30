package telemetry

import (
	"context"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestStartSpanWithoutTracerIsNoop(t *testing.T) {
	ctx := context.Background()
	spanCtx, span := StartSpan(ctx, SpanRequest)
	if spanCtx != ctx {
		t.Error("StartSpan without a tracer must return the context unchanged")
	}
	if span.IsRecording() {
		t.Error("StartSpan without a tracer must return a non-recording span")
	}
	span.SetAttributes(attribute.String(AttrProvider, "openai"))
	span.End()
}

func TestNilTracingIsNoopEverywhere(t *testing.T) {
	var tracing *Tracing
	ctx := context.Background()
	if got := tracing.Extract(ctx, http.Header{}); got != ctx {
		t.Error("nil Tracing Extract must return the context unchanged")
	}
	if got := ContextWithTracing(ctx, tracing); got != ctx {
		t.Error("ContextWithTracing with a nil tracer must return the context unchanged")
	}
	if err := tracing.Shutdown(ctx); err != nil {
		t.Errorf("nil Tracing Shutdown = %v, want nil", err)
	}
}

func TestStartSpanExportsThroughConfiguredTracer(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracing := NewTracingWithExporter(exporter)
	ctx := ContextWithTracing(context.Background(), tracing)

	spanCtx, span := StartSpan(ctx, SpanRequest, attribute.String(AttrProvider, "openai"))
	if !span.IsRecording() {
		t.Fatal("span under a configured tracer must record")
	}
	AnnotateSpanTimings(spanCtx, 3, 250)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	got := spans[0]
	if got.Name != SpanRequest {
		t.Errorf("span name = %q, want %q", got.Name, SpanRequest)
	}
	attrs := make(map[attribute.Key]attribute.Value, len(got.Attributes))
	for _, kv := range got.Attributes {
		attrs[kv.Key] = kv.Value
	}
	if v, ok := attrs[AttrProvider]; !ok || v.AsString() != "openai" {
		t.Errorf("attribute %s = %v, want openai", AttrProvider, v.String())
	}
	if v, ok := attrs[AttrOverheadMS]; !ok || v.AsInt64() != 3 {
		t.Errorf("attribute %s = %v, want 3", AttrOverheadMS, v.String())
	}
	if v, ok := attrs[AttrTTFTMS]; !ok || v.AsInt64() != 250 {
		t.Errorf("attribute %s = %v, want 250", AttrTTFTMS, v.String())
	}
}

func TestAnnotateSpanTimingsSkipsZeroValues(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracing := NewTracingWithExporter(exporter)
	ctx := ContextWithTracing(context.Background(), tracing)

	spanCtx, span := StartSpan(ctx, SpanRequest)
	AnnotateSpanTimings(spanCtx, 5, 0)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	for _, kv := range spans[0].Attributes {
		if kv.Key == AttrTTFTMS {
			t.Error("a zero TTFT must not land on the span")
		}
	}
}

func TestExtractContinuesInboundTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracing := NewTracingWithExporter(exporter)

	header := http.Header{}
	header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	ctx := tracing.Extract(context.Background(), header)

	spanContext := trace.SpanContextFromContext(ctx)
	if got := spanContext.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("extracted trace ID = %q, want the inbound one", got)
	}

	ctx = ContextWithTracing(ctx, tracing)
	_, span := StartSpan(ctx, SpanRequest)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	if got := spans[0].SpanContext.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("span trace ID = %q, want the inbound one", got)
	}
	if got := spans[0].Parent.SpanID().String(); got != "00f067aa0ba902b7" {
		t.Errorf("span parent = %q, want the inbound span ID", got)
	}
}

func TestTracesConfiguredReadsStandardEnvironment(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	if TracesConfigured() {
		t.Error("TracesConfigured with no environment = true, want false")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:4318/v1/traces")
	if !TracesConfigured() {
		t.Error("TracesConfigured with the traces endpoint set = false, want true")
	}
}
