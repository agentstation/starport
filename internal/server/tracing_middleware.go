package server

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"

	"github.com/agentstation/starport/internal/telemetry"
)

// TracingMiddleware starts the request span and carries the tracer into the
// request context, where the routing and execution seams start their children.
// It continues the caller's W3C trace context when the inbound headers carry
// one. A nil tracer leaves the handler chain untouched.
func TracingMiddleware(tracing *telemetry.Tracing) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if tracing == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := tracing.Extract(r.Context(), r.Header)
			ctx = telemetry.ContextWithTracing(ctx, tracing)
			ctx, span := telemetry.StartSpan(ctx, telemetry.SpanRequest,
				attribute.String("http.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			)
			defer span.End()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r.WithContext(ctx))
			span.SetAttributes(attribute.Int("http.status_code", wrapped.Status()))
		})
	}
}
