package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/agentstation/starport/internal/telemetry"
)

func TestTracingMiddlewareStartsRequestSpanAndContinuesInboundTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracing := telemetry.NewTracingWithExporter(exporter)

	handler := TracingMiddleware(tracing)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != telemetry.SpanRequest {
		t.Errorf("span name = %q, want %q", span.Name, telemetry.SpanRequest)
	}
	if got := span.SpanContext.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("span trace ID = %q, want the inbound one", got)
	}
	status := int64(0)
	for _, kv := range span.Attributes {
		if string(kv.Key) == "http.status_code" {
			status = kv.Value.AsInt64()
		}
	}
	if status != http.StatusTeapot {
		t.Errorf("http.status_code attribute = %d, want %d", status, http.StatusTeapot)
	}
}

func TestTracingMiddlewareWithNilTracerLeavesHandlerUntouched(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := TracingMiddleware(nil)(inner)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
}
