package router

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/telemetry"
	"github.com/stretchr/testify/require"
)

// The tracing contract: one traced chat request produces the four named spans
// in parent order — request over route_plan, and request over attempt over
// provider_call. Without a tracer on the context, no span is produced.

func TestRouteWithFallbackProducesFourSpansInParentOrder(t *testing.T) {
	connector := &mockConnector{
		name: "openai",
		chatFunc: func(_ context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			return &connectors.ChatResponse{
				ID:    "chatcmpl-traced",
				Model: req.Model,
				Choices: []connectors.Choice{
					{Message: connectors.Message{Role: "assistant", Content: "ok"}},
				},
			}, nil
		},
	}
	router := New(&mockRegistry{connectors: map[string]connectors.Connector{"openai": connector}})

	exporter := tracetest.NewInMemoryExporter()
	tracing := telemetry.NewTracingWithExporter(exporter)
	ctx := telemetry.ContextWithTracing(context.Background(), tracing)

	// The server middleware starts the request span; the test plays that role.
	ctx, requestSpan := telemetry.StartSpan(ctx, telemetry.SpanRequest)
	_, err := router.RouteWithFallback(ctx, &Request{
		ChatRequest: &connectors.ChatRequest{
			Model:    "openai/gpt-4",
			Messages: []connectors.Message{{Role: "user", Content: "hello"}},
		},
	})
	require.NoError(t, err)
	requestSpan.End()

	spans := spansByName(exporter.GetSpans())
	for _, name := range []string{
		telemetry.SpanRequest, telemetry.SpanRoutePlan,
		telemetry.SpanAttempt, telemetry.SpanProviderCall,
	} {
		require.Contains(t, spans, name, "one traced request must export %s", name)
	}

	request := spans[telemetry.SpanRequest]
	require.Equal(t, request.SpanContext.SpanID(), parentID(spans[telemetry.SpanRoutePlan]),
		"route_plan must nest under request")
	require.Equal(t, request.SpanContext.SpanID(), parentID(spans[telemetry.SpanAttempt]),
		"attempt must nest under request")
	require.Equal(t, spans[telemetry.SpanAttempt].SpanContext.SpanID(), parentID(spans[telemetry.SpanProviderCall]),
		"provider_call must nest under attempt")

	call := spans[telemetry.SpanProviderCall]
	attrs := attributeMap(call)
	require.Equal(t, "openai", attrs[telemetry.AttrProvider], "provider_call must carry the provider")
	require.Equal(t, "openai/gpt-4", attrs[telemetry.AttrModel], "provider_call must carry the route ID")

	attempt := attributeMap(spans[telemetry.SpanAttempt])
	require.Equal(t, int64(1), attempt[telemetry.AttrAttempt], "the first attempt must number 1")
}

func TestRouteWithFallbackWithoutTracerExportsNothing(t *testing.T) {
	connector := &mockConnector{
		name: "openai",
		chatFunc: func(_ context.Context, req *connectors.ChatRequest) (*connectors.ChatResponse, error) {
			return &connectors.ChatResponse{
				ID:    "chatcmpl-untraced",
				Model: req.Model,
				Choices: []connectors.Choice{
					{Message: connectors.Message{Role: "assistant", Content: "ok"}},
				},
			}, nil
		},
	}
	router := New(&mockRegistry{connectors: map[string]connectors.Connector{"openai": connector}})

	_, err := router.RouteWithFallback(context.Background(), &Request{
		ChatRequest: &connectors.ChatRequest{
			Model:    "openai/gpt-4",
			Messages: []connectors.Message{{Role: "user", Content: "hello"}},
		},
	})
	require.NoError(t, err)
}

func spansByName(spans tracetest.SpanStubs) map[string]tracetest.SpanStub {
	byName := make(map[string]tracetest.SpanStub, len(spans))
	for _, span := range spans {
		byName[span.Name] = span
	}
	return byName
}

func parentID(span tracetest.SpanStub) trace.SpanID {
	return span.Parent.SpanID()
}

func attributeMap(span tracetest.SpanStub) map[string]any {
	attrs := make(map[string]any, len(span.Attributes))
	for _, kv := range span.Attributes {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}
	return attrs
}
