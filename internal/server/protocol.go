package server

import (
	"context"
	"net/http"

	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/protocol/openrouter"
)

type protocolContextKey struct{}

type wireProtocol string

const (
	openAIProtocol     wireProtocol = "openai"
	openRouterProtocol wireProtocol = "openrouter"
)

func selectProtocol(protocol wireProtocol) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), protocolContextKey{}, protocol)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeProtocolError(w http.ResponseWriter, r *http.Request, status int, errorType, message string) {
	if protocol, ok := r.Context().Value(protocolContextKey{}).(wireProtocol); ok && protocol == openRouterProtocol {
		openrouter.WriteError(w, status, message, map[string]any{"error_type": errorType})
		return
	}
	openai.WriteError(w, status, errorType, message, nil)
}
